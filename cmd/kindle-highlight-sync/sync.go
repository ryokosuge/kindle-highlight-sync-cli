package main

import (
	"context"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/lock"
)

const lockFileName = "run.lock"

// syncBody は排他の内側で行う sync の処理。この Task では何もしない。
// テストは差し替えて先発を止めておく。後続の Epic がここに処理を足す。
var syncBody = func(ctx context.Context, a *app) error { return nil }

func newSyncCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Kindle の Highlight と Note を Notion に同期する",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) (err error) {
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
			return syncBody(cmd.Context(), a)
		},
	}
}
