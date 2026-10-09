package main

import (
	"context"
	"testing"
	"time"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/source"
)

// fakeBrowser は openBrowser の差し替えが返すブラウザ。CI に Chrome は無いので、本物は開かない。
type fakeBrowser struct {
	waitErr  error // WaitNotebook が返す
	closeErr error // Close が返す
	onClose  func()

	waitDeadline time.Time // WaitNotebook に渡った ctx の期限（無ければゼロ値）
	waitCalledAt time.Time
	closed       bool
}

func (f *fakeBrowser) OpenNotebook(context.Context) (bool, error) {
	return false, nil
}

func (f *fakeBrowser) WaitNotebook(ctx context.Context) error {
	f.waitCalledAt = time.Now()
	f.waitDeadline, _ = ctx.Deadline()
	return f.waitErr
}

func (f *fakeBrowser) Close() error {
	f.closed = true
	if f.onClose != nil {
		f.onClose()
	}
	return f.closeErr
}

// browserStub は差し替えた openBrowser に渡った値。
type browserStub struct {
	opened int
	opts   source.Options // 最後に渡った Options
}

// stubBrowser は openBrowser を fake に差し替える。openErr が nil 以外なら b を返さずに失敗する。
// race detector のため、goroutine を起動する前に呼ぶ。
func stubBrowser(t *testing.T, b *fakeBrowser, openErr error) *browserStub {
	t.Helper()
	s := &browserStub{}
	orig := openBrowser
	openBrowser = func(_ context.Context, opts source.Options) (browser, error) {
		s.opened++
		s.opts = opts
		if openErr != nil {
			return nil, openErr
		}
		return b, nil
	}
	t.Cleanup(func() { openBrowser = orig })
	return s
}
