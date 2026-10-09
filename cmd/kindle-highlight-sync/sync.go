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
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withLock(a, func() error { return syncBody(cmd.Context(), a) })
		},
	}
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
