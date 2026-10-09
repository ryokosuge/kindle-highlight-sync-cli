package source

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// signInURL は Notebook を cookie なしで開いたときに送られる認証ページ（仕様書 §18.8）。fragment は確かめのために足した。
const signInURL = "https://www.amazon.co.jp/ap/signin?openid.pape.max_auth_age=3600&openid.return_to=https%3A%2F%2Fread.amazon.co.jp%2Fnotebook&openid.assoc_handle=amzn_kindle_ynhv2_jp&openid.mode=checkid_setup&language=ja_JP#secret-fragment"

func TestIsNotebook(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"Notebook", "https://read.amazon.co.jp/notebook", true},
		{"クエリ付きの Notebook", "https://read.amazon.co.jp/notebook?asin=B000000000&contentLimitState=", true},
		{"認証ページ", "https://www.amazon.co.jp/ap/signin", false},
		{"return_to に Notebook を含む認証ページ", "https://www.amazon.co.jp/ap/signin?openid.return_to=https%3A%2F%2Fread.amazon.co.jp%2Fnotebook", false},
		{"http", "http://read.amazon.co.jp/notebook", false},
		{"別 host", "https://www.amazon.co.jp/notebook", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNotebook(tt.url); got != tt.want {
				t.Errorf("isNotebook(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestNotReached(t *testing.T) {
	err := notReached(signInURL, context.DeadlineExceeded)
	want := `source: kindle notebook not reached (last URL "www.amazon.co.jp/ap/signin"): context deadline exceeded`
	if err.Error() != want {
		t.Errorf("notReached().Error() = %q, want %q", err.Error(), want)
	}
	if !errors.Is(err, ErrNotebookNotReached) {
		t.Errorf("errors.Is(err, ErrNotebookNotReached) = false, err = %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("errors.Is(err, context.DeadlineExceeded) = false, err = %v", err)
	}
}

// fakeAmazon は Chrome の Fetch で read.amazon.co.jp と www.amazon.co.jp への要求に答える（ネットワークにも Amazon にも出ない）。
// signedIn でなければ Notebook は認証ページへ 302 で送る。
type fakeAmazon struct {
	signedIn atomic.Bool
	// signInOnSignInPage が true なら、認証ページを返したところで signedIn にし、ページの JS で Notebook に戻る（本人のサインインに当たる）。
	signInOnSignInPage bool
	// library が nil でなければ、サインイン済みの Notebook はクエリごとの HTML を返し、受けたクエリを libraryQueries に残す。
	library map[string][]byte
	mu      sync.Mutex
	queries []string
}

// libraryQueries は、これまで Notebook に来たクエリ（u.RawQuery）を順番に返す。
func (f *fakeAmazon) libraryQueries() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.queries)
}

func (f *fakeAmazon) respond(raw string) fetch.FulfillRequestParams {
	u, err := url.Parse(raw)
	if err != nil {
		return fetch.FulfillRequestParams{ResponseCode: 400}
	}
	html := []*fetch.HeaderEntry{{Name: "Content-Type", Value: "text/html; charset=utf-8"}}
	switch u.Host + u.Path {
	case "read.amazon.co.jp/notebook":
		if f.signedIn.Load() {
			if f.library == nil {
				return fetch.FulfillRequestParams{ResponseCode: 200, ResponseHeaders: html, Body: []byte("<p>notebook</p>")}
			}
			f.mu.Lock()
			f.queries = append(f.queries, u.RawQuery)
			f.mu.Unlock()
			body, ok := f.library[u.RawQuery]
			if !ok {
				return fetch.FulfillRequestParams{ResponseCode: 404}
			}
			return fetch.FulfillRequestParams{ResponseCode: 200, ResponseHeaders: html, Body: body}
		}
		return fetch.FulfillRequestParams{ResponseCode: 302, ResponseHeaders: []*fetch.HeaderEntry{{Name: "Location", Value: signInURL}}}
	case "www.amazon.co.jp/ap/signin":
		body := "<p>sign in</p>"
		if f.signInOnSignInPage {
			f.signedIn.Store(true)
			body += `<script>setTimeout(() => { location.href = "https://read.amazon.co.jp/notebook" }, 1000)</script>`
		}
		return fetch.FulfillRequestParams{ResponseCode: 200, ResponseHeaders: html, Body: []byte(body)}
	}
	return fetch.FulfillRequestParams{ResponseCode: 404}
}

// openFakeAmazon は実 Chrome を開き、要求に f で答えるようにする。Close はテストの終わりにする。
func openFakeAmazon(t *testing.T, f *fakeAmazon) *Browser {
	t.Helper()
	b, err := Open(context.Background(), Options{Headless: true, ProfileDir: filepath.Join(t.TempDir(), "browser-profile"), ExecPath: chromePath(t)})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	tgt := chromedp.FromContext(b.ctx).Target
	paused := cdp.Events(b.ctx, tgt, fetch.RequestPaused)
	go func() {
		for ev, err := range paused {
			if err != nil {
				return
			}
			p := f.respond(ev.Request.URL)
			p.RequestID = ev.RequestID
			// ブラウザが先に閉じたときの失敗は、テストの結果に関わらないので捨てる。
			_, _ = cdp.Call(b.ctx, tgt, fetch.FulfillRequest, p)
		}
	}()
	if _, err := cdp.Call(b.ctx, tgt, fetch.Enable, fetch.EnableParams{}); err != nil {
		t.Fatalf("Fetch.enable error = %v", err)
	}
	return b
}

func TestOpenNotebookChrome(t *testing.T) {
	chromePath(t)

	t.Run("サインイン済みなら Notebook に届く", func(t *testing.T) {
		f := &fakeAmazon{}
		f.signedIn.Store(true)
		b := openFakeAmazon(t, f)
		got, err := b.OpenNotebook(context.Background())
		if err != nil {
			t.Fatalf("OpenNotebook() error = %v", err)
		}
		if !got {
			t.Error("OpenNotebook() = false, want true")
		}
	})

	t.Run("認証ページに送られたら届かない", func(t *testing.T) {
		b := openFakeAmazon(t, &fakeAmazon{})
		got, err := b.OpenNotebook(context.Background())
		if err != nil {
			t.Fatalf("OpenNotebook() error = %v", err)
		}
		if got {
			t.Error("OpenNotebook() = true, want false")
		}
	})
}

func TestPageChrome(t *testing.T) {
	chromePath(t)

	t.Run("サインイン済みなら Notebook の URL と HTML を返す", func(t *testing.T) {
		f := &fakeAmazon{}
		f.signedIn.Store(true)
		b := openFakeAmazon(t, f)
		if _, err := b.OpenNotebook(context.Background()); err != nil {
			t.Fatalf("OpenNotebook() error = %v", err)
		}
		finalURL, html, err := b.Page(context.Background())
		if err != nil {
			t.Fatalf("Page() error = %v", err)
		}
		if finalURL != NotebookURL {
			t.Errorf("Page() finalURL = %q, want %q", finalURL, NotebookURL)
		}
		if !strings.Contains(string(html), "<p>notebook</p>") {
			t.Errorf("Page() html = %q, want it to contain %q", html, "<p>notebook</p>")
		}
	})

	t.Run("認証ページに送られたら認証ページの URL と HTML を返す", func(t *testing.T) {
		b := openFakeAmazon(t, &fakeAmazon{})
		if _, err := b.OpenNotebook(context.Background()); err != nil {
			t.Fatalf("OpenNotebook() error = %v", err)
		}
		finalURL, html, err := b.Page(context.Background())
		if err != nil {
			t.Fatalf("Page() error = %v", err)
		}
		if finalURL != signInURL {
			t.Errorf("Page() finalURL = %q, want %q", finalURL, signInURL)
		}
		if !strings.Contains(string(html), "<p>sign in</p>") {
			t.Errorf("Page() html = %q, want it to contain %q", html, "<p>sign in</p>")
		}
	})
}

func TestWaitNotebookChrome(t *testing.T) {
	chromePath(t)

	t.Run("認証ページからサインインして Notebook に戻ったら nil を返す", func(t *testing.T) {
		b := openFakeAmazon(t, &fakeAmazon{signInOnSignInPage: true})
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := b.WaitNotebook(ctx); err != nil {
			t.Fatalf("WaitNotebook() error = %v", err)
		}
	})

	t.Run("届く前に context が切れたら ErrNotebookNotReached を返し、文言にクエリと fragment を出さない", func(t *testing.T) {
		b := openFakeAmazon(t, &fakeAmazon{})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err := b.WaitNotebook(ctx)
		if !errors.Is(err, ErrNotebookNotReached) {
			t.Fatalf("WaitNotebook() error = %v, want ErrNotebookNotReached", err)
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("errors.Is(err, context.DeadlineExceeded) = false, err = %v", err)
		}
		want := `source: kindle notebook not reached (last URL "www.amazon.co.jp/ap/signin"): context deadline exceeded`
		if err.Error() != want {
			t.Errorf("WaitNotebook() error = %q, want %q", err.Error(), want)
		}
		for _, leak := range []string{"?", "openid", "return_to", "#", "secret-fragment"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("WaitNotebook() error = %q, want no %q", err.Error(), leak)
			}
		}
	})

	t.Run("届く前にブラウザが閉じられたら ErrNotebookNotReached を返す", func(t *testing.T) {
		b := openFakeAmazon(t, &fakeAmazon{})
		done := make(chan error, 1)
		go func() { done <- b.WaitNotebook(context.Background()) }()
		time.Sleep(2 * pollInterval)
		// 本人が window を閉じるのと同じく、tab を閉じる。
		tid := chromedp.FromContext(b.ctx).Target.TargetID
		if _, err := chromedp.CallBrowser(b.ctx, target.CloseTarget, target.CloseTargetParams{TargetID: tid}); err != nil {
			t.Fatalf("Target.closeTarget error = %v", err)
		}
		select {
		case err := <-done:
			if !errors.Is(err, ErrNotebookNotReached) {
				t.Fatalf("WaitNotebook() error = %v, want ErrNotebookNotReached", err)
			}
			want := `source: kindle notebook not reached (last URL "www.amazon.co.jp/ap/signin"): browser closed`
			if err.Error() != want {
				t.Errorf("WaitNotebook() error = %q, want %q", err.Error(), want)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("WaitNotebook() did not return after the browser closed")
		}
	})
}
