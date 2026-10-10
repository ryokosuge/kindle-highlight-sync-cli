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

// Destination は §6.2 のうち、照合だけを持つ。書き込みの method は Epic #7 で足す。
type Destination interface {
	FindBook(ctx context.Context, book domain.Book) (BookMatch, error)
}
