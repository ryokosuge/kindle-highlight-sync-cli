package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/lock"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/source"
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

// syncBody は排他の内側で行う sync の処理。login の profile を headless（--headed なら画面付き）で開き、Notebook 到達後に Amazon の認証を確かめる。
// テストは差し替えて先発を止めておく。後続の Epic が Highlight の取得と Notion への同期をここに足す。
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
	return nil
}

func newSyncCmd(a *app) *cobra.Command {
	var headed bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Kindle の Highlight と Note を Notion に同期する",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withLock(a, func() error { return syncBody(cmd.Context(), a, headed) })
		},
	}
	cmd.Flags().BoolVar(&headed, "headed", false, "画面付きの Chrome で同期する（既定は headless）")
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
