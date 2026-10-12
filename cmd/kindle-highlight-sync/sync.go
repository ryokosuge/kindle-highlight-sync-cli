package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/lock"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/source"
	syncengine "github.com/ryokosuge/kindle-highlight-sync-cli/internal/sync"
)

const lockFileName = "run.lock"

// syncNotebookWait は headless の Chrome で Notebook を開き終えるのを待つ上限。login と違い本人の操作を待たない。
const syncNotebookWait = time.Minute

// checkAuth は Notebook を開いたあとのページから Amazon の認証を確かめ、切れていれば exitcode.AmazonAuth（2）で包んだ失敗を返す。
// 判定不能は認証切れにしない（Notebook の構造の異常は SourceFormatError で止める）。
var checkAuth = func(ctx context.Context, b browser) error {
	finalURL, html, err := b.Page(ctx)
	if err != nil {
		return err
	}
	if state, reason := source.DetectAuth(finalURL, html); state == source.AuthExpired {
		return exitcode.New(exitcode.AmazonAuth, fmt.Errorf("sync: %s; run `kindle-highlight-sync login` to sign in again", reason))
	}
	return nil
}

// newSyncPrompter は Initial Sync の確認の入出力を作る。テストは入力を差し替える。
var newSyncPrompter = newPrompter

// bookFailureKind は Book 単位の失敗の種類。Book ID と一緒に標準エラーに出すので、Title や Highlight の本文・Note は持たない（仕様書 §74）。
type bookFailureKind string

const (
	bookFailureSourceFormat bookFailureKind = "unexpected notebook structure" // その Book の Highlight のページだけが構造の異常（§55 / §72）
	bookFailureNoASIN       bookFailureKind = "book has no ASIN"              // ASIN が無く Highlight のページを開けない Book（Book ID は §13 の b1-）
)

// bookFailure は Highlight を取れなかった 1 冊の Book。
type bookFailure struct {
	BookID string
	Kind   bookFailureKind
}

// booksFailedError は Book 単位の失敗を集めたもの。残りの Book を取り終えてから返す。
type booksFailedError struct {
	failed []bookFailure
	books  int // 取得を試みた Book の総数
}

func (e *booksFailedError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "sync: highlights of %d of %d books could not be listed:", len(e.failed), e.books)
	for i, f := range e.failed {
		if i > 0 {
			b.WriteString(";")
		}
		fmt.Fprintf(&b, " %s (%s)", f.BookID, f.Kind)
	}
	return b.String()
}

// syncBody は排他の内側で行う sync の処理。Notebook 到達後に Amazon の認証を確かめ、Book 一覧を最後のページまで取得し、Book ごとに全 Highlight を取得する。
// a.dryRun なら Highlight の取得は照合（BuildPlan）の中で 1 回だけ行い、予定の表を a.out に出して Notion と State は書かない（§63）。
// initial_sync_completed_at が無ければ Initial Sync として、予定を組んで Create / Adopt の件数の確認を取ってから取得に進み（§47）、
// 確認の後ろで none の Book を作成し adoptable の Book を Adopt して、件数を出す（§51）。skip・書き込みの失敗は exit 8（§72）。取得を終えた run を記録する（§48）。
var syncBody = func(ctx context.Context, a *app, headed bool) (err error) {
	// Open に渡す ctx にタイムアウトを付けると Chrome ごと止まるので、Notebook を開く action だけに付ける
	b, err := openBrowser(ctx, source.Options{
		ProfileDir: a.paths.BrowserProfileDir(),
		Headless:   !headed,
	})
	if err != nil {
		return err // 起動の失敗は exitcode.Browser（5）で包まれている
	}
	var record bool // Initial Sync で確認を通過し、取得を終えた（success か Book 単位の失敗）
	defer func() {
		// 本体の失敗を優先し、本体が成功したときだけ閉じる失敗を返す
		cerr := b.Close()
		if err == nil {
			err = cerr
		}
		// 記録は run の最終結果が確定した後、閉じるのにも成功したときだけ行う（§48）。閉じる失敗（exit 5）は success でも partial でもないので記録しない。
		// 記録の失敗（exit 6）は本体が成功したときだけ返す
		if !record || cerr != nil {
			return
		}
		if merr := a.store.MarkInitialSyncCompleted(ctx, time.Now()); err == nil {
			err = merr
		}
	}()

	octx, cancel := context.WithTimeout(ctx, syncNotebookWait)
	defer cancel()
	reached, err := b.OpenNotebook(octx)
	if err != nil {
		return err
	}
	if err := checkAuth(octx, b); err != nil {
		return err
	}
	if !reached {
		// 認証切れでないのに届かなかったときは成功扱いにしない
		return fmt.Errorf("sync: %w", source.ErrNotebookNotReached)
	}
	// 全ページを辿る取得には Notebook を開く待ちの期限を付けない
	books, err := b.ListBooks(ctx)
	if err != nil {
		return err
	}
	if a.dryRun {
		return writeDryRunPlan(ctx, a, books, b)
	}
	_, synced, err := a.store.InitialSyncCompletedAt(ctx)
	if err != nil {
		return err // state.db を読めない失敗は exitcode.LocalState（6）で包まれている
	}
	if !synced {
		// Initial Sync（§47）。予定の算出（BuildPlan）は Book 単位の失敗を許さないので、下の取得ループには流用せず Highlight を 2 回取る。
		// 予定から除いた ASIN の無い Book は下のループでも同じ失敗に数えるので、ここでは捨てる
		plan, _, err := buildSyncPlan(ctx, a, books, b)
		if err != nil {
			return err
		}
		if err := confirmInitialSync(a.prompt, plan.Summary, a.prompt.interactive(), a.yes); err != nil {
			return err
		}
	}
	// 確認（Initial Sync なら）を通ったあと、Initial Sync かどうかにかかわらず none の Book は作成し、adoptable の Book は Adopt する（§51）。
	// ambiguous の skip と書き込みの失敗は、下の Book 単位の失敗の後ろで exit 8 にする
	applied, err := applyBooks(ctx, a, books)
	if err != nil {
		return err
	}
	// 取得した Highlight はまだ使わない。1 冊だけの構造の異常と ASIN の無い Book は Book ID と種類を集めて残りの Book を続け、
	// 取り終えて 1 冊でもあれば SourceFormat（exit 7）で終わる（§72。exit 8 への切り替えと last_synced_at の扱いは Epic #7）。
	// 全 Book に共通の枠の異常（source.ErrHighlightsFrame。§73）とそれ以外の失敗（取得の失敗・ctx の取消）は、その時点で種類を変えずに返す
	var failed []bookFailure
	for _, book := range books {
		if book.ASIN == "" {
			// Highlight のページは ASIN でしか開けない。黙って飛ばすと取りこぼしに気づけないので失敗に数える
			failed = append(failed, bookFailure{BookID: book.ID, Kind: bookFailureNoASIN})
			continue
		}
		_, err := b.ListHighlights(ctx, book)
		switch {
		case err == nil:
		case errors.Is(err, source.ErrSourceFormat) && !errors.Is(err, source.ErrHighlightsFrame):
			failed = append(failed, bookFailure{BookID: book.ID, Kind: bookFailureSourceFormat})
		default:
			return err
		}
	}
	// 確認を通過して取得を終えた run は、success でも Book 単位の失敗（partial、§72）でも閉じたあとに記録する（上の defer）。
	// 全 Book に共通の失敗はここに届かず記録しない
	record = !synced
	if len(failed) > 0 {
		// ASIN の無い Book（exit 7）と skip・書き込みの失敗（exit 8）が両方あれば exit 7 を返す
		return exitcode.New(exitcode.SourceFormat, &booksFailedError{failed: failed, books: len(books)})
	}
	return applied.PartialFailure()
}

// applyBooks は ASIN の無い Book を除いて Book ごとに作成・Adopt を実行し、件数を a.out に出す。
// 同期先の組み立て（exit 3）と照合・書き込みの fatal な失敗（exit 4）は種類を変えずに返す。
// skip・Book 単位の失敗は結果に残し、呼び出し側が PartialFailure で exit 8 にする。
func applyBooks(ctx context.Context, a *app, books []domain.Book) (syncengine.ApplyResult, error) {
	dst, err := newDestination(a)
	if err != nil {
		return syncengine.ApplyResult{}, err
	}
	targets := make([]domain.Book, 0, len(books))
	for _, book := range books {
		if book.ASIN != "" {
			targets = append(targets, book)
		}
	}
	result, err := syncengine.ApplyBooks(ctx, targets, dst)
	if err != nil {
		return result, err
	}
	return result, writeApplyResult(a.out, result)
}

// writeDryRunPlan は dry-run の予定の表と summary を a.out に出す（§63 / §64）。
// 予定を組めなければ（exit 3 / exit 4）種類を変えずに返して表は出さない。ASIN の無い Book があれば、
// 残りの Book の予定を出したあとに SourceFormat（exit 7）で終わる（§72）。
func writeDryRunPlan(ctx context.Context, a *app, books []domain.Book, src browser) error {
	plan, failed, err := buildSyncPlan(ctx, a, books, src)
	if err != nil {
		return err
	}
	if err := writePlan(a.out, plan); err != nil {
		return err
	}
	if len(failed) > 0 {
		return exitcode.New(exitcode.SourceFormat, &booksFailedError{failed: failed, books: len(books)})
	}
	return nil
}

// buildSyncPlan は Book を Notion と照合して予定を組み、予定から除いた Book を失敗として返す。
// ASIN の無い Book は Highlight のページを開けないので、通常の sync と同じく bookFailureNoASIN に集めて照合から除く。
// 同期先の組み立て（exit 3）と照合（exit 4）の失敗は種類を変えずに返す。
func buildSyncPlan(ctx context.Context, a *app, books []domain.Book, src browser) (syncengine.Plan, []bookFailure, error) {
	dst, err := newDestination(a)
	if err != nil {
		return syncengine.Plan{}, nil, err
	}
	var failed []bookFailure
	planned := make([]domain.Book, 0, len(books))
	for _, book := range books {
		if book.ASIN == "" {
			failed = append(failed, bookFailure{BookID: book.ID, Kind: bookFailureNoASIN})
			continue
		}
		planned = append(planned, book)
	}
	plan, err := syncengine.BuildPlan(ctx, planned, src, dst)
	if err != nil {
		return syncengine.Plan{}, nil, err
	}
	return plan, failed, nil
}

func newSyncCmd(a *app) *cobra.Command {
	var headed bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Kindle の Highlight と Note を Notion に同期する",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a.out = cmd.OutOrStdout()
			a.prompt = newSyncPrompter(cmd)
			a.store = newStateStore(a.paths)
			return withLock(a, func() error { return syncBody(cmd.Context(), a, headed) })
		},
	}
	cmd.Flags().BoolVar(&headed, "headed", false, "画面付きの Chrome で同期する（既定は headless）")
	cmd.Flags().BoolVar(&a.dryRun, "dry-run", false, "Notion と State を変えずに、Book の照合の結果と追記する Highlight の数だけを出す")
	cmd.Flags().BoolVar(&a.yes, "yes", false, "Initial Sync の Create / Adopt の確認を省く（入力が端末でないときに要る）")
	return cmd
}

// withLock は run.lock の排他を取って body を呼び、終わったら排他を解放する。
// sync と login が同じ排他の内側で動くための共通の入口。
func withLock(a *app, body func() error) (err error) {
	l, err := lock.Acquire(filepath.Join(a.paths.DataDir, lockFileName))
	if err != nil {
		return err // 取り合いなら exitcode.AlreadyRunning（9）で包まれている
	}
	defer func() {
		// 本体の失敗を優先し、本体が成功したときだけ解放の失敗を返す
		if rerr := l.Release(); err == nil {
			err = rerr
		}
	}()
	return body()
}
