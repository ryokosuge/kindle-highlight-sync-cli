package source

import (
	"context"
	"fmt"

	"github.com/chromedp/chromedp"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
)

// fetchPage は Book 一覧のページの HTML を返す。token が空なら 1 ページ目。テストで差し替える。
type fetchPage func(ctx context.Context, token string) ([]byte, error)

// ListBooks は Notebook の Book 一覧を最後のページまで辿り、全 Book を返す。
// 途中で失敗したら、それまでの Book は返さない（仕様書 §56）。
// 構造の異常（root や続きの印が無い・同じ token の再出現）は SourceFormat の種類で返し、
// ページの取得そのものの失敗（ブラウザのエラー・ctx 切れ）は種類を付けずに返す。
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
		p, err := parseLibraryResponse(html, token)
		if err != nil {
			return nil, fmt.Errorf("source: list books: page %d: %w", n, err)
		}
		books = append(books, p.Books...)
		if p.NextToken == "" {
			return books, nil
		}
		if seen[p.NextToken] {
			return nil, fmt.Errorf("source: list books: page %d: %w", n, newFormatError("next page token was already used"))
		}
		seen[p.NextToken] = true
		token = p.NextToken
	}
}

// parseLibraryResponse は token が空なら 1 ページ目として、それ以外は続きの断片として解析する（仕様書 §18.4）。
func parseLibraryResponse(html []byte, token string) (LibraryPage, error) {
	if token == "" {
		return ParseLibraryPage(html)
	}
	return ParseLibraryFragment(html)
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
