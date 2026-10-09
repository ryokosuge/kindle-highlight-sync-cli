package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/source"
)

// loginWait は Notebook に到達するのを待つ上限。MFA の入力を含めても足りる長さにしている。
const loginWait = 10 * time.Minute

const loginInstruction = "ブラウザで Amazon にログインしてください"

// loginBody は排他の内側で行う login の処理。テストは差し替えて先発を止めておく。
var loginBody = func(ctx context.Context, a *app, out io.Writer) (err error) {
	// Open に渡す ctx にタイムアウトを付けると Chrome ごと止まるので、待ちだけに付ける
	b, err := openBrowser(ctx, source.Options{
		ProfileDir: a.paths.BrowserProfileDir(),
		Headless:   false,
	})
	if err != nil {
		return err // 起動の失敗は exitcode.Browser（5）で包まれている
	}
	defer func() {
		// 待ちの失敗を優先し、待ちが成功したときだけ閉じる失敗を返す
		if cerr := b.Close(); err == nil {
			err = cerr
		}
	}()
	_, _ = fmt.Fprintln(out, loginInstruction)

	wctx, cancel := context.WithTimeout(ctx, loginWait)
	defer cancel()
	if err := b.WaitNotebook(wctx); err != nil {
		if errors.Is(err, source.ErrNotebookNotReached) {
			return exitcode.New(exitcode.AmazonAuth, err)
		}
		return err
	}
	return nil
}

func newLoginCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "画面付きの Chrome で Amazon にログインし、sync が使うセッションを保存する",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withLock(a, func() error { return loginBody(cmd.Context(), a, cmd.OutOrStdout()) })
		},
	}
}
