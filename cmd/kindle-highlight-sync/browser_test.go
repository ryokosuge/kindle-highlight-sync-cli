package main

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/source"
)

// fakeBrowser は openBrowser の差し替えが返すブラウザ。CI に Chrome は無いので、本物は開かない。
type fakeBrowser struct {
	reached      bool  // OpenNotebook が返す
	openErr      error // OpenNotebook が返す
	waitErr      error // WaitNotebook が返す
	closeErr     error // Close が返す
	onClose      func()
	pageURL      string
	pageHTML     []byte
	pageErr      error
	pageCalled   int
	books        []domain.Book
	listErr      error
	listCalled   int
	listDeadline time.Time
	highlights   map[string][]domain.Highlight // Book.ID ごとに ListHighlights が返す
	highlightErr map[string]error              // Book.ID ごとに ListHighlights が返す
	laterHLErr   map[string]error              // Book.ID ごとに、2 回目以降の ListHighlights が返す（予定の算出では取れ、実行で失敗する Book）
	hlBooks      []domain.Book                 // ListHighlights に渡った Book の順
	hlDeadlines  []time.Time                   // ListHighlights に渡った ctx の期限（無ければゼロ値）
	calls        []string                      // 呼ばれたメソッドの順

	openDeadline time.Time // OpenNotebook に渡った ctx の期限（無ければゼロ値）
	openCalledAt time.Time
	waitDeadline time.Time // WaitNotebook に渡った ctx の期限（無ければゼロ値）
	waitCalledAt time.Time
	closed       bool
}

func (f *fakeBrowser) OpenNotebook(ctx context.Context) (bool, error) {
	f.calls = append(f.calls, "OpenNotebook")
	f.openCalledAt = time.Now()
	f.openDeadline, _ = ctx.Deadline()
	return f.reached, f.openErr
}

func (f *fakeBrowser) Page(context.Context) (string, []byte, error) {
	f.calls = append(f.calls, "Page")
	f.pageCalled++
	return f.pageURL, f.pageHTML, f.pageErr
}

func (f *fakeBrowser) ListBooks(ctx context.Context) ([]domain.Book, error) {
	f.calls = append(f.calls, "ListBooks")
	f.listCalled++
	f.listDeadline, _ = ctx.Deadline()
	return f.books, f.listErr
}

func (f *fakeBrowser) ListHighlights(ctx context.Context, book domain.Book) ([]domain.Highlight, error) {
	f.calls = append(f.calls, "ListHighlights")
	f.hlBooks = append(f.hlBooks, book)
	d, _ := ctx.Deadline()
	f.hlDeadlines = append(f.hlDeadlines, d)
	if err := f.laterHLErr[book.ID]; err != nil && slices.ContainsFunc(f.hlBooks[:len(f.hlBooks)-1], func(b domain.Book) bool { return b.ID == book.ID }) {
		return f.highlights[book.ID], err
	}
	return f.highlights[book.ID], f.highlightErr[book.ID]
}

func (f *fakeBrowser) WaitNotebook(ctx context.Context) error {
	f.calls = append(f.calls, "WaitNotebook")
	f.waitCalledAt = time.Now()
	f.waitDeadline, _ = ctx.Deadline()
	return f.waitErr
}

func (f *fakeBrowser) Close() error {
	f.calls = append(f.calls, "Close")
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
