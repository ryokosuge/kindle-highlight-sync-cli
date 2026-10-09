package source

import (
	"context"
	"errors"
	"fmt"

	"github.com/chromedp/chromedp"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
)

// errLibraryTokenRepeated は、続きの token がすでに使ったものと同じだったときの失敗。token の値は文言に出さない。
var errLibraryTokenRepeated = errors.New("next page token was already used")

// fetchPage は Book 一覧のページの HTML を返す。token が空なら 1 ページ目。テストで差し替える。
type fetchPage func(ctx context.Context, token string) ([]byte, error)

// ListBooks は Notebook の Book 一覧を最後のページまで辿り、全 Book を返す。
// 途中で失敗したら、それまでの Book は返さない（仕様書 §56）。
func (b *Browser) ListBooks(ctx context.Context) ([]domain.Book, error) {
	return listBooks(ctx, b.fetchLibraryPage)
}

func listBooks(ctx context.Context, fetch fetchPage) ([]domain.Book, error) {
	seen := map[string]bool{}
	var books []domain.Book
	var token string
	for n := 1; ; n++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("source: list books: page %d: %w", n, err)
		}
		html, err := fetch(ctx, token)
		if err != nil {
			return nil, fmt.Errorf("source: list books: page %d: %w", n, err)
		}
		p, err := ParseLibraryPage(html)
		if err != nil {
			return nil, fmt.Errorf("source: list books: page %d: %w", n, err)
		}
		books = append(books, p.Books...)
		if p.NextToken == "" {
			return books, nil
		}
		if seen[p.NextToken] {
			return nil, fmt.Errorf("source: list books: page %d: %w", n, errLibraryTokenRepeated)
		}
		seen[p.NextToken] = true
		token = p.NextToken
	}
}

// libraryURL は Book 一覧のページの URL を返す。token はすでに URL エンコードされている（%2B・%3D を含む）ので、
// url.Values や QueryEscape で二度エンコードしない。
func libraryURL(token string) string {
	if token == "" {
		return NotebookURL
	}
	return NotebookURL + "?library=list&token=" + token
}

func (b *Browser) fetchLibraryPage(ctx context.Context, token string) ([]byte, error) {
	rctx, cancel := b.withContext(ctx)
	defer cancel()
	if err := chromedp.Do(rctx, chromedp.Navigate(libraryURL(token))); err != nil {
		return nil, fmt.Errorf("open library page: %w", err)
	}
	html, err := chromedp.Run(rctx, chromedp.OuterHTML("html"))
	if err != nil {
		return nil, fmt.Errorf("read library page: %w", err)
	}
	return []byte(html), nil
}
