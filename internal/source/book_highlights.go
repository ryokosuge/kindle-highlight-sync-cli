package source

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/chromedp/chromedp"
)

// errHighlightsASINEmpty は ASIN が空の Book を渡されたときの失敗。ASIN が空の Book は Highlight のページを取れない。
var errHighlightsASINEmpty = errors.New("source: list highlights: book has no ASIN")

// errHighlightsTokenRepeated は、続きの token がすでに使ったものと同じだったときの失敗。token の値は文言に出さない。
var errHighlightsTokenRepeated = errors.New("next page token was already used")

// fetchHighlightsPage は Book の Highlight のページの HTML を返す。token が空なら 1 ページ目。テストで差し替える。
type fetchHighlightsPage func(ctx context.Context, asin, token, contentLimitState string) ([]byte, error)

// listHighlightEntries は 1 冊の Book の Highlight のページを token をたどって最後まで取り、全行を返す。
// Book をまたぐ繰り返しはしない。途中で失敗したら、それまでの行は返さない（仕様書 §56）。
// 続きはスクロールではなく token で取る（§18.6）。
func listHighlightEntries(ctx context.Context, asin string, fetch fetchHighlightsPage) ([]HighlightEntry, error) {
	if asin == "" {
		return nil, errHighlightsASINEmpty
	}
	seen := map[string]bool{}
	var entries []HighlightEntry
	var token string
	var cls string
	for n := 1; ; n++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("source: list highlights %s: page %d: %w", asin, n, err)
		}
		html, err := fetch(ctx, asin, token, cls)
		if err != nil {
			return nil, fmt.Errorf("source: list highlights %s: page %d: %w", asin, n, err)
		}
		p, err := ParseHighlightsPage(html)
		if err != nil {
			return nil, fmt.Errorf("source: list highlights %s: page %d: %w", asin, n, err)
		}
		entries = append(entries, p.Entries...)
		if p.ContentLimitState != "" {
			cls = p.ContentLimitState
		}
		if p.NextToken == "" {
			return entries, nil
		}
		if seen[p.NextToken] {
			return nil, fmt.Errorf("source: list highlights %s: page %d: %w", asin, n, errHighlightsTokenRepeated)
		}
		seen[p.NextToken] = true
		token = p.NextToken
	}
}

// highlightsURL は Book の Highlight のページの URL を返す（形は仕様書 §18.5）。
// ASIN は生の値なので QueryEscape する。token・contentLimitState は HTML の value 属性そのもので
// URL エンコード済みのため連結で組み、二度エンコードしない。1 ページ目のように空ならそのクエリを付けない。
func highlightsURL(asin, token, contentLimitState string) string {
	u := NotebookURL + "?asin=" + url.QueryEscape(asin)
	if contentLimitState != "" {
		u += "&contentLimitState=" + contentLimitState
	}
	if token != "" {
		u += "&token=" + token
	}
	return u
}

func (b *Browser) fetchHighlightsPage(ctx context.Context, asin, token, contentLimitState string) ([]byte, error) {
	rctx, cancel := b.withContext(ctx)
	defer cancel()
	if err := chromedp.Do(rctx, chromedp.Navigate(highlightsURL(asin, token, contentLimitState))); err != nil {
		return nil, fmt.Errorf("open highlights page: %w", err)
	}
	html, err := chromedp.Run(rctx, chromedp.OuterHTML("html"))
	if err != nil {
		return nil, fmt.Errorf("read highlights page: %w", err)
	}
	return []byte(html), nil
}
