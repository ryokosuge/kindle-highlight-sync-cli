// Package sync は Source と Destination をつなぐ同期の処理（仕様書 §4 の Sync Engine）を持つ。
package sync

import (
	"context"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/destination"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
)

// Action は §64 の Action 列の値。
type Action string

const (
	// ActionAppend は照合済みの Book に追記する Action。
	ActionAppend Action = "append"
	// ActionAdoptAndAppend は Kindle ID Property が空の既存ページを Adopt して追記する Action。
	ActionAdoptAndAppend Action = "adopt + append"
	// ActionCreateAndAppend は同期先にページを新しく作って追記する Action。
	ActionCreateAndAppend Action = "create + append"
	// ActionSkip は照合が ambiguous な Book の Action（§51 で skip）。
	ActionSkip Action = "skip"
)

// BookPlan は Book 1 冊分の同期予定。
type BookPlan struct {
	Book               domain.Book
	Match              destination.BookMatch
	Action             Action
	HighlightsToAppend int
}

// Summary は Plan 全体の集計（§64 の summary 表の行）。
type Summary struct {
	ExactMatches, AdoptableMatches, NewBooks, Ambiguous, HighlightsToAppend int
}

// Plan は sync の書き込み前に見せる全体の予定。
type Plan struct {
	Books   []BookPlan
	Summary Summary
}

// highlightLister は Source から Book の Highlight を取る。テストで差し替える。
type highlightLister interface {
	ListHighlights(ctx context.Context, book domain.Book) ([]domain.Highlight, error)
}

// BuildPlan は書き込まずに、各 Book の照合の結果と追記する Highlight の数を集める（仕様書 §63）。
func BuildPlan(ctx context.Context, books []domain.Book, src highlightLister, dst destination.Destination) (Plan, error) {
	p := Plan{Books: make([]BookPlan, 0, len(books))}
	for _, b := range books {
		m, err := dst.FindBook(ctx, b)
		if err != nil {
			return Plan{}, err
		}
		bp := BookPlan{Book: b, Match: m}
		switch m.Status {
		case destination.BookMatchExact:
			bp.Action = ActionAppend
			p.Summary.ExactMatches++
		case destination.BookMatchAdoptable:
			bp.Action = ActionAdoptAndAppend
			p.Summary.AdoptableMatches++
		case destination.BookMatchNone:
			bp.Action = ActionCreateAndAppend
			p.Summary.NewBooks++
		case destination.BookMatchAmbiguous:
			bp.Action = ActionSkip
			p.Summary.Ambiguous++
		}
		if bp.Action != ActionSkip {
			hs, err := src.ListHighlights(ctx, b)
			if err != nil {
				return Plan{}, err
			}
			bp.HighlightsToAppend = len(hs)
			p.Summary.HighlightsToAppend += bp.HighlightsToAppend
		}
		p.Books = append(p.Books, bp)
	}
	return p, nil
}
