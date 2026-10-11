// Package destination は同期先の抽象（仕様書 §6.2）と Book の照合（§9）を持つ。
package destination

import (
	"context"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
)

// RemoteBook は同期先の Book のページ。MVP では ID は Notion Page ID（§8）。
type RemoteBook struct {
	ID string
}

// RemoteHighlight は同期先の Highlight。MVP では ID は Highlight を表す親 Quote Block の Notion Block ID（§8）。
type RemoteHighlight struct {
	ID          string
	HighlightID string
}

type BookMatchStatus string

const (
	BookMatchNone      BookMatchStatus = "none"
	BookMatchExact     BookMatchStatus = "exact"
	BookMatchAdoptable BookMatchStatus = "adoptable"
	BookMatchAmbiguous BookMatchStatus = "ambiguous"
)

// BookMatch は照合の結果。Book は exact / adoptable のときだけ非 nil。
type BookMatch struct {
	Status BookMatchStatus
	Book   *RemoteBook
}

// Destination は §6.2 のうち、照合・Book のページの作成と Adopt・exact のページの更新・Highlight ID の一覧・Highlight の追記を持つ。
// AppendHighlights は部分成功を許容し、error のときも追記できた Highlight を返す（§30）。
type Destination interface {
	FindBook(ctx context.Context, book domain.Book) (BookMatch, error)
	CreateBook(ctx context.Context, book domain.Book) (RemoteBook, error)
	AdoptBook(ctx context.Context, remote RemoteBook, book domain.Book) error
	UpdateBook(ctx context.Context, remote RemoteBook, book domain.Book) error
	ListHighlightIDs(ctx context.Context, remote RemoteBook) (map[string]RemoteHighlight, error)
	AppendHighlights(ctx context.Context, remote RemoteBook, highlights []domain.Highlight) ([]RemoteHighlight, error)
}
