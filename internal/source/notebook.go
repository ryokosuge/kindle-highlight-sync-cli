package source

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// NotebookURL は Kindle Notebook（仕様書 §18、Amazon.co.jp）。
const NotebookURL = "https://read.amazon.co.jp/notebook"

// pollInterval は WaitNotebook が最終 URL を見に行く間隔。
const pollInterval = 500 * time.Millisecond

// ErrNotebookNotReached は Kindle Notebook に届かなかったときの失敗。exit code の種類は呼び出し側が決める。
var ErrNotebookNotReached = errors.New("kindle notebook not reached")

// errBrowserClosed は、届く前にブラウザが閉じられたときに ErrNotebookNotReached に添える理由。
var errBrowserClosed = errors.New("browser closed")

// isNotebook は raw の scheme・host・path が NotebookURL と同じかを返す。
// クエリは見ないので、認証ページ（www.amazon.co.jp/ap/signin）は return_to に Notebook を含んでいても届いていない。
func isNotebook(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	n, err := url.Parse(NotebookURL)
	return err == nil && u.Scheme == n.Scheme && u.Host == n.Host && u.Path == n.Path
}

// OpenNotebook は Kindle Notebook を開き、読み込み終えたときの最終 URL が Notebook かを返す。
// 認証ページに送られたら false と nil を返す。
func (b *Browser) OpenNotebook(ctx context.Context) (bool, error) {
	rctx, cancel := b.withContext(ctx)
	defer cancel()
	if err := chromedp.Do(rctx, chromedp.Navigate(NotebookURL)); err != nil {
		return false, fmt.Errorf("source: open notebook: %w", err)
	}
	loc, err := chromedp.Run(rctx, chromedp.Location())
	if err != nil {
		return false, fmt.Errorf("source: open notebook: %w", err)
	}
	return isNotebook(loc), nil
}

// WaitNotebook は Kindle Notebook を開き、最終 URL が Notebook になるまで pollInterval ごとに見続ける（login で本人がサインインし終えるのを待つ）。
// 届いたら nil を返す。届く前に ctx が切れたかブラウザが閉じられたら ErrNotebookNotReached を返す。
func (b *Browser) WaitNotebook(ctx context.Context) error {
	rctx, cancel := b.withContext(ctx)
	defer cancel()
	if err := chromedp.Do(rctx, chromedp.Navigate(NotebookURL)); err != nil && rctx.Err() == nil {
		return fmt.Errorf("source: open notebook: %w", err)
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	var last string
	for {
		// 読み込みの途中は Location が失敗することがあるので、次の回でまた見る。
		if loc, err := chromedp.Run(rctx, chromedp.Location()); err == nil {
			last = loc
			if isNotebook(loc) {
				return nil
			}
		}
		select {
		case <-rctx.Done():
			if err := ctx.Err(); err != nil {
				return notReached(last, err)
			}
			return notReached(last, errBrowserClosed)
		case <-ticker.C:
		}
	}
}

// withContext は b の Chrome に送る context を返す。ctx が切れたときも、ブラウザが閉じられたときも終わる。
// Chrome のプロセスが終われば b.ctx が切れる。macOS では最後の window を閉じても Chrome が残るので、tab が閉じられたこと（Target.detachedFromTarget）も見る。
func (b *Browser) withContext(ctx context.Context) (context.Context, context.CancelFunc) {
	rctx, cancel := context.WithCancel(b.ctx)
	stop := context.AfterFunc(ctx, cancel)
	c := chromedp.FromContext(b.ctx)
	detached := cdp.Events(rctx, c.Browser, target.DetachedFromTarget)
	go func() {
		for ev, err := range detached {
			if err != nil {
				return
			}
			if ev.SessionID == c.Target.SessionID {
				cancel()
				return
			}
		}
	}()
	return rctx, func() {
		stop()
		cancel()
	}
}

// notReached は ErrNotebookNotReached を包み、最後に見た URL を host と path だけで添える。
// 認証ページのクエリと fragment には return_to や session の値が入るので、文言に出さない。
func notReached(last string, cause error) error {
	return fmt.Errorf("source: %w (last URL %q): %w", ErrNotebookNotReached, hostPath(last), cause)
}

func hostPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host + u.Path
}
