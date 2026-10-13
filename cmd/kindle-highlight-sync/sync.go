package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
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

// syncBody は排他の内側で行う sync の処理。Notebook 到達後に Amazon の認証を確かめ、Book 一覧を最後のページまで取得し、Book ごとに全 Highlight を取得して追記する。
// a.dryRun なら Highlight の取得は照合（BuildPlan）の中で 1 回だけ行い、予定の表を a.out に出して Notion と State は書かない（§63）。
// initial_sync_completed_at が無ければ Initial Sync として、予定を組んで Create / Adopt の件数の確認を取ってから取得に進み（§47）、
// 確認の後ろで none の Book を作成し adoptable の Book を Adopt して、件数を出す（§51）。skip・書き込みの失敗は exit 8（§72）。取得を終えた run を記録する（§48）。
var syncBody = func(ctx context.Context, a *app, headed bool) (err error) {
	startedAt := time.Now() // Highlight と Book の LastSyncedAt に入れる、sync の開始時刻
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
	// 対象の選択は dry-run の分岐より前に 1 回だけ行う（§53 / §54）。照合（FindBook）が対象外の Book まで走らないようにする
	total := len(books)
	books, err = selectTargets(ctx, a, books)
	if err != nil {
		return err
	}
	if a.dryRun {
		return writeDryRunPlan(ctx, a, books, total, b)
	}
	_, synced, err := a.store.InitialSyncCompletedAt(ctx)
	if err != nil {
		return err // state.db を読めない失敗は exitcode.LocalState（6）で包まれている
	}
	var plan syncengine.Plan // Initial Sync で確認した予定。その失敗は実行の結果に合流する
	if !synced {
		// Initial Sync（§47）。実行の取得には予定を流用せず Highlight を予定と実行で 1 回ずつ取る。
		// 予定の算出で失敗に残った Book は確認した件数の外なので、実行には渡さず（applyBooks）、失敗として実行の結果に合流させる
		plan, err = buildSyncPlan(ctx, a, books, b)
		if err != nil {
			return err
		}
		if err := confirmInitialSync(a.prompt, plan.Summary, a.prompt.interactive(), a.yes); err != nil {
			return err
		}
	}
	// 確認（Initial Sync なら）を通ったあと、Initial Sync かどうかにかかわらず none の Book は作成し、adoptable の Book は Adopt し、
	// ページが決まった Book には既知を除いた Highlight を追記する（§51 / §24）。Highlight は ApplyBooks が Book ごとに取る。
	// 取得の失敗の仕分けは ApplyBooks に任せる。1 冊だけの構造の異常と Highlight ID が作れない失敗は Failed に載って続き、
	// 全 Book に共通の枠の異常（§73）・ブラウザの失敗・ctx の取消と期限切れ・ローカル状態の不整合は種類を変えずにここへ返る
	applied, err := applyBooks(ctx, a, books, plan.Failed, total, b, startedAt)
	if err != nil {
		return err
	}
	// 確認を通過して取得を終えた run は、success でも Book 単位の失敗（partial、§72）でも閉じたあとに記録する（上の defer）。
	// 全 Book に共通の失敗はここに届かず記録しない
	record = !synced
	return applied.PartialFailure()
}

// selectTargets は State の books の last_synced_at から、この run で同期する Book を選ぶ。--full なら全 Book を返す。
// State は読むだけで、state.db が無ければ作らない。読めなければ exitcode.LocalState（6）で包まれた失敗を返す。
func selectTargets(ctx context.Context, a *app, books []domain.Book) ([]domain.Book, error) {
	recorded, err := a.store.ListBooks(ctx)
	if err != nil {
		return nil, err
	}
	lastSynced := make(map[string]time.Time, len(recorded))
	for id, b := range recorded {
		lastSynced[id] = b.LastSyncedAt
	}
	return syncengine.SelectTargets(books, lastSynced, a.full), nil
}

// applyBooks は planFailed（予定の算出で失敗に残った Book）を除いた Book ごとに作成・Adopt・追記を実行し、件数と対象の数（books の数と全 Book の数 total）を a.out に出す。
// ASIN の無い Book の失敗は ApplyBooks が数える。planFailed の Book は実行せず、結果の Failed の先頭に合流させる。
// 同期先の組み立て（exit 3）と照合・書き込みの fatal な失敗（exit 4）、全 Book に共通の取得の失敗は種類を変えずに返す。
// skip・Book 単位の失敗は結果に残し、呼び出し側が PartialFailure（exit 8）にする。
func applyBooks(ctx context.Context, a *app, books []domain.Book, planFailed []syncengine.FailedBook, total int, src browser, startedAt time.Time) (syncengine.ApplyResult, error) {
	dst, err := newDestination(a)
	if err != nil {
		return syncengine.ApplyResult{}, err
	}
	failedIDs := make(map[string]bool, len(planFailed))
	for _, f := range planFailed {
		failedIDs[f.BookID] = true
	}
	targets := make([]domain.Book, 0, len(books))
	for _, book := range books {
		if !failedIDs[book.ID] {
			targets = append(targets, book)
		}
	}
	result, err := syncengine.ApplyBooks(ctx, targets, src, dst, a.store, startedAt)
	result.Failed = append(slices.Clone(planFailed), result.Failed...)
	// fatal な失敗で止まっても、そこまでに書いた分は Notion に残るので件数を出してから error を返す
	if werr := writeApplyResult(a.out, result); err == nil {
		err = werr
	}
	if werr := writeTargetCount(a.out, len(books), total); err == nil {
		err = werr
	}
	return result, err
}

// writeDryRunPlan は dry-run の予定の表と summary を a.out に出す（§63 / §64）。
// 予定を組めなければ（exit 3 / exit 4）種類を変えずに返して表は出さない。ASIN の無い Book か Highlight の取得が失敗した Book があれば、
// 残りの Book の予定を出したあとに PartialFailure（exit 8）で終わる（§72）。
func writeDryRunPlan(ctx context.Context, a *app, books []domain.Book, total int, src browser) error {
	plan, err := buildSyncPlan(ctx, a, books, src)
	if err != nil {
		return err
	}
	if err := writePlan(a.out, plan); err != nil {
		return err
	}
	if err := writeTargetCount(a.out, len(books), total); err != nil {
		return err
	}
	if len(plan.Failed) > 0 {
		return syncengine.NewFailedBooksError(plan.Failed)
	}
	return nil
}

// buildSyncPlan は Book を Notion と照合して予定を組む。ASIN の無い Book と 1 冊の取得の失敗は BuildPlan が Plan.Failed に残す。
// 同期先の組み立て（exit 3）と照合（exit 4）の失敗は種類を変えずに返す。
func buildSyncPlan(ctx context.Context, a *app, books []domain.Book, src browser) (syncengine.Plan, error) {
	dst, err := newDestination(a)
	if err != nil {
		return syncengine.Plan{}, err
	}
	return syncengine.BuildPlan(ctx, books, src, dst)
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
	cmd.Flags().BoolVar(&a.full, "full", false, "State の記録に関わらず全 Book の Highlight を取って同期する（既定は変化のある Book と未同期の Book だけ）")
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
