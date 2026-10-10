package source

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/chromedp/chromedp"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
)

// errHighlightsASINEmpty は ASIN が空の Book を渡されたときの失敗。ASIN が空の Book は Highlight のページを取れない。
var errHighlightsASINEmpty = errors.New("source: list highlights: book has no ASIN")

// fetchHighlightsPage は Book の Highlight のページの HTML を返す。token が空なら 1 ページ目。テストで差し替える。
type fetchHighlightsPage func(ctx context.Context, asin, token, contentLimitState string) ([]byte, error)

// ListHighlights は Book の全 Highlight を a- の Highlight ID 付きで返す。
// 途中で失敗したら、それまでの Highlight は返さない（仕様書 §56）。
// ページ取得の失敗は種類を変えずに返す。続きの token の異常・行 id から ID が作れない・ID の重複・空の行は
// 構造の異常として SourceFormat（exit 7）の種類で返す（仕様書 §16.1 / §19 / §55）。行を飛ばして残りを返すこともしない。
func (b *Browser) ListHighlights(ctx context.Context, book domain.Book) ([]domain.Highlight, error) {
	return listHighlights(ctx, book, b.fetchHighlightsPage)
}

func listHighlights(ctx context.Context, book domain.Book, fetch fetchHighlightsPage) ([]domain.Highlight, error) {
	entries, err := listHighlightEntries(ctx, book.ASIN, fetch)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	hs := make([]domain.Highlight, 0, len(entries))
	for i, e := range entries {
		id, err := domain.NewHighlightID(e.RawID, book.ASIN)
		if err != nil {
			// 行 id が読めない行に別方式の ID を作らない（§16.1）。原因（domain.ErrMissingAnnotationID）も errors.Is で辿れるよう残す。
			return nil, fmt.Errorf("source: list highlights %s: row %d: %w", book.ASIN, i+1, wrapFormatError(err))
		}
		if seen[id] {
			// 行 id の重複でだけ起きる。黙ってまとめると Note が消えるのでエラーにする。
			return nil, fmt.Errorf("source: list highlights %s: row %d: %w", book.ASIN, i+1, newFormatError("highlight ID was already returned"))
		}
		seen[id] = true
		hs = append(hs, domain.Highlight{
			ID:       id,
			BookID:   book.ID,
			Text:     e.Text,
			Note:     e.Note,
			Location: e.Location,
			Page:     e.Page,
		})
	}
	return hs, nil
}

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
			return nil, fmt.Errorf("source: list highlights %s: page %d: %w", asin, n, newFormatError("next page token was already used"))
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
