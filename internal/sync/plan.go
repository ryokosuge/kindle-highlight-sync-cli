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
	// Failed は予定を組めなかった Book（ASIN が無い Book と、その Book だけの Highlight の取得の失敗）。Books と Summary には入らない。ambiguous は skip の行として Books に出るのでここには入れない。
	Failed []FailedBook
}

// highlightLister は Source から Book の Highlight を取る。テストで差し替える。
type highlightLister interface {
	ListHighlights(ctx context.Context, book domain.Book) ([]domain.Highlight, error)
}

// BuildPlan は書き込まずに、各 Book の照合の結果と追記する Highlight の数を集める（仕様書 §63）。
// Notion にページがある Book（exact / adoptable）は、Sync Marker で既知の Highlight ID を除いた数だけを数える（§26 の appendIDs = sourceIDs - knownIDs。
// この Epic では State を読まないので knownIDs は remoteIDs だけ）。
// ASIN の無い Book は照合せず FailureNoASIN として Failed に残し、Highlight の取得が ClassifyFetchError で Book 単位と判定される error で失敗した Book は種類付きで Failed に残して、残りの Book の予定を組み続ける。
// それ以外の error（FindBook / ListHighlightIDs の失敗・枠の異常・ctx の取消・ブラウザ由来）はそのまま返して止まる。none はページが無く、ambiguous は Highlight を取らないので、どちらも Notion の block を読まない。
func BuildPlan(ctx context.Context, books []domain.Book, src highlightLister, dst destination.Destination) (Plan, error) {
	p := Plan{Books: make([]BookPlan, 0, len(books))}
	for _, b := range books {
		if b.ASIN == "" {
			p.Failed = append(p.Failed, FailedBook{BookID: b.ID, Kind: FailureNoASIN})
			continue
		}
		m, err := dst.FindBook(ctx, b)
		if err != nil {
			return Plan{}, err
		}
		bp := BookPlan{Book: b, Match: m}
		var hasPage bool
		switch m.Status {
		case destination.BookMatchExact:
			bp.Action = ActionAppend
			hasPage = true
		case destination.BookMatchAdoptable:
			bp.Action = ActionAdoptAndAppend
			hasPage = true
		case destination.BookMatchNone:
			bp.Action = ActionCreateAndAppend
		case destination.BookMatchAmbiguous:
			bp.Action = ActionSkip
		}
		if bp.Action != ActionSkip {
			var known map[string]destination.RemoteHighlight
			if hasPage {
				if known, err = dst.ListHighlightIDs(ctx, *m.Book); err != nil {
					return Plan{}, err
				}
			}
			hs, err := src.ListHighlights(ctx, b)
			if err != nil {
				kind, ok := ClassifyFetchError(err)
				if !ok {
					return Plan{}, err
				}
				p.Failed = append(p.Failed, FailedBook{BookID: b.ID, Kind: kind})
				continue
			}
			bp.HighlightsToAppend = countUnknown(hs, known)
			p.Summary.HighlightsToAppend += bp.HighlightsToAppend
		}
		switch m.Status {
		case destination.BookMatchExact:
			p.Summary.ExactMatches++
		case destination.BookMatchAdoptable:
			p.Summary.AdoptableMatches++
		case destination.BookMatchNone:
			p.Summary.NewBooks++
		case destination.BookMatchAmbiguous:
			p.Summary.Ambiguous++
		}
		p.Books = append(p.Books, bp)
	}
	return p, nil
}

// unknownHighlights は known に Highlight ID が無い Highlight を、hs の順のまま返す。known にだけある ID は結果に影響しない。
func unknownHighlights[V any](hs []domain.Highlight, known map[string]V) []domain.Highlight {
	var unknown []domain.Highlight
	for _, h := range hs {
		if _, ok := known[h.ID]; !ok {
			unknown = append(unknown, h)
		}
	}
	return unknown
}

// countUnknown は known に Highlight ID が無い Highlight の数。
func countUnknown(hs []domain.Highlight, known map[string]destination.RemoteHighlight) int {
	return len(unknownHighlights(hs, known))
}
