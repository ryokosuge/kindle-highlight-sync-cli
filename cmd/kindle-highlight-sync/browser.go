package main

import (
	"context"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/source"
)

// browser は login と sync が開く Chrome。*source.Browser が満たす。
type browser interface {
	OpenNotebook(ctx context.Context) (bool, error)
	Page(ctx context.Context) (finalURL string, html []byte, err error)
	ListBooks(ctx context.Context) ([]domain.Book, error)
	ListHighlights(ctx context.Context, book domain.Book) ([]domain.Highlight, error)
	WaitNotebook(ctx context.Context) error
	Close() error
}

// openBrowser は Chrome を起動する。CI に Chrome は無いので、テストは fake に差し替える。
var openBrowser = func(ctx context.Context, opts source.Options) (browser, error) {
	b, err := source.Open(ctx, opts)
	if err != nil {
		return nil, err // 型付きの nil を interface に入れない
	}
	return b, nil
}
