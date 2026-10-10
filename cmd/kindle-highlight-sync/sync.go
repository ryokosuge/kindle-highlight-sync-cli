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
// テストは差し替えて先発を止めておく。後続の Epic が Notion への同期をここに足す。
var syncBody = func(ctx context.Context, a *app, headed bool) (err error) {
	// Open に渡す ctx にタイムアウトを付けると Chrome ごと止まるので、Notebook を開く action だけに付ける
	b, err := openBrowser(ctx, source.Options{
		ProfileDir: a.paths.BrowserProfileDir(),
		Headless:   !headed,
	})
	if err != nil {
		return err // 起動の失敗は exitcode.Browser（5）で包まれている
	}
	defer func() {
		// 本体の失敗を優先し、本体が成功したときだけ閉じる失敗を返す
		if cerr := b.Close(); err == nil {
			err = cerr
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
	if len(failed) > 0 {
		return exitcode.New(exitcode.SourceFormat, &booksFailedError{failed: failed, books: len(books)})
	}
	return nil
}

// writeDryRunPlan は Book を Notion と照合して予定を組み、表と summary を a.out に書く（§63 / §64）。
// 同期先の組み立て（exit 3）と照合（exit 4）の失敗は種類を変えずに返し、予定は出さない。
// writeDryRunPlan は dry-run の予定を a.out に出す。ASIN の無い Book は Highlight のページを開けないので、通常の sync と同じく
// bookFailureNoASIN に集めて予定の組み立てから除き、残りの Book の予定を出したあとに SourceFormat（exit 7）で終わる（§72）。
func writeDryRunPlan(ctx context.Context, a *app, books []domain.Book, src browser) error {
	dst, err := newDestination(a)
	if err != nil {
		return err
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

func newSyncCmd(a *app) *cobra.Command {
	var headed bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Kindle の Highlight と Note を Notion に同期する",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a.out = cmd.OutOrStdout()
			return withLock(a, func() error { return syncBody(cmd.Context(), a, headed) })
		},
	}
	cmd.Flags().BoolVar(&headed, "headed", false, "画面付きの Chrome で同期する（既定は headless）")
	cmd.Flags().BoolVar(&a.dryRun, "dry-run", false, "Notion と State を変えずに、Book の照合の結果と追記する Highlight の数だけを出す")
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
