package sync

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/destination"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
)

type fakeDestination struct {
	matches map[string]destination.BookMatch
	errs    map[string]error
	calls   []string
}

func (f *fakeDestination) FindBook(_ context.Context, book domain.Book) (destination.BookMatch, error) {
	f.calls = append(f.calls, book.ID)
	if err, ok := f.errs[book.ID]; ok {
		return destination.BookMatch{}, err
	}
	return f.matches[book.ID], nil
}

func (f *fakeDestination) ListHighlightIDs(_ context.Context, _ destination.RemoteBook) (map[string]destination.RemoteHighlight, error) {
	return nil, nil
}

type fakeSource struct {
	highlights map[string][]domain.Highlight
	errs       map[string]error
	calls      []string
}

func (f *fakeSource) ListHighlights(_ context.Context, book domain.Book) ([]domain.Highlight, error) {
	f.calls = append(f.calls, book.ID)
	if err, ok := f.errs[book.ID]; ok {
		return nil, err
	}
	return f.highlights[book.ID], nil
}

func highlights(ids ...string) []domain.Highlight {
	hs := make([]domain.Highlight, 0, len(ids))
	for _, id := range ids {
		hs = append(hs, domain.Highlight{ID: id, Text: "本文"})
	}
	return hs
}

func TestBuildPlan(t *testing.T) {
	tests := []struct {
		name          string
		books         []domain.Book
		matches       map[string]destination.BookMatch
		highlights    map[string][]domain.Highlight
		want          Plan
		wantListCalls []string
	}{
		{
			name:  "exact の Book は append でページ ID と件数が入る",
			books: []domain.Book{{ID: "book-1", Title: "Go 本"}},
			matches: map[string]destination.BookMatch{
				"book-1": {Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "page-1"}},
			},
			highlights: map[string][]domain.Highlight{
				"book-1": highlights("a-1", "a-2"),
			},
			want: Plan{
				Books: []BookPlan{{
					Book:               domain.Book{ID: "book-1", Title: "Go 本"},
					Match:              destination.BookMatch{Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "page-1"}},
					Action:             ActionAppend,
					HighlightsToAppend: 2,
				}},
				Summary: Summary{ExactMatches: 1, HighlightsToAppend: 2},
			},
			wantListCalls: []string{"book-1"},
		},
		{
			name:  "adoptable の Book は adopt + append でページが入る",
			books: []domain.Book{{ID: "book-1", Title: "Go 本"}},
			matches: map[string]destination.BookMatch{
				"book-1": {Status: destination.BookMatchAdoptable, Book: &destination.RemoteBook{ID: "page-9"}},
			},
			highlights: map[string][]domain.Highlight{
				"book-1": highlights("a-1"),
			},
			want: Plan{
				Books: []BookPlan{{
					Book:               domain.Book{ID: "book-1", Title: "Go 本"},
					Match:              destination.BookMatch{Status: destination.BookMatchAdoptable, Book: &destination.RemoteBook{ID: "page-9"}},
					Action:             ActionAdoptAndAppend,
					HighlightsToAppend: 1,
				}},
				Summary: Summary{AdoptableMatches: 1, HighlightsToAppend: 1},
			},
			wantListCalls: []string{"book-1"},
		},
		{
			name:  "none の Book は create + append でページは空のまま",
			books: []domain.Book{{ID: "book-1", Title: "Go 本"}},
			matches: map[string]destination.BookMatch{
				"book-1": {Status: destination.BookMatchNone},
			},
			highlights: map[string][]domain.Highlight{
				"book-1": highlights("a-1", "a-2", "a-3"),
			},
			want: Plan{
				Books: []BookPlan{{
					Book:               domain.Book{ID: "book-1", Title: "Go 本"},
					Match:              destination.BookMatch{Status: destination.BookMatchNone},
					Action:             ActionCreateAndAppend,
					HighlightsToAppend: 3,
				}},
				Summary: Summary{NewBooks: 1, HighlightsToAppend: 3},
			},
			wantListCalls: []string{"book-1"},
		},
		{
			name:  "ambiguous の Book は skip で Highlight を取りに行かない",
			books: []domain.Book{{ID: "book-1", Title: "Go 本"}},
			matches: map[string]destination.BookMatch{
				"book-1": {Status: destination.BookMatchAmbiguous},
			},
			highlights: map[string][]domain.Highlight{
				"book-1": highlights("a-1", "a-2"),
			},
			want: Plan{
				Books: []BookPlan{{
					Book:   domain.Book{ID: "book-1", Title: "Go 本"},
					Match:  destination.BookMatch{Status: destination.BookMatchAmbiguous},
					Action: ActionSkip,
				}},
				Summary: Summary{Ambiguous: 1},
			},
			wantListCalls: nil,
		},
		{
			name:  "Note だけの Highlight も 1 件と数える",
			books: []domain.Book{{ID: "book-1", Title: "Go 本"}},
			matches: map[string]destination.BookMatch{
				"book-1": {Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "page-1"}},
			},
			highlights: map[string][]domain.Highlight{
				"book-1": {{ID: "a-1", Note: "メモ"}},
			},
			want: Plan{
				Books: []BookPlan{{
					Book:               domain.Book{ID: "book-1", Title: "Go 本"},
					Match:              destination.BookMatch{Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "page-1"}},
					Action:             ActionAppend,
					HighlightsToAppend: 1,
				}},
				Summary: Summary{ExactMatches: 1, HighlightsToAppend: 1},
			},
			wantListCalls: []string{"book-1"},
		},
		{
			name: "複数 Book の summary は各予定の合計",
			books: []domain.Book{
				{ID: "b1"},
				{ID: "b2"},
				{ID: "b3"},
				{ID: "b4"},
				{ID: "b5"},
				{ID: "b6"},
			},
			matches: map[string]destination.BookMatch{
				"b1": {Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "p1"}},
				"b2": {Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "p2"}},
				"b3": {Status: destination.BookMatchAdoptable, Book: &destination.RemoteBook{ID: "p3"}},
				"b4": {Status: destination.BookMatchNone},
				"b5": {Status: destination.BookMatchAmbiguous},
				"b6": {Status: destination.BookMatchAmbiguous},
			},
			highlights: map[string][]domain.Highlight{
				"b1": highlights("a-1"),
				"b2": highlights("a-2", "a-3"),
				"b3": highlights("a-4"),
				"b5": highlights("a-5"),
				"b6": highlights("a-6"),
			},
			want: Plan{
				Books: []BookPlan{
					{Book: domain.Book{ID: "b1"}, Match: destination.BookMatch{Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "p1"}}, Action: ActionAppend, HighlightsToAppend: 1},
					{Book: domain.Book{ID: "b2"}, Match: destination.BookMatch{Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "p2"}}, Action: ActionAppend, HighlightsToAppend: 2},
					{Book: domain.Book{ID: "b3"}, Match: destination.BookMatch{Status: destination.BookMatchAdoptable, Book: &destination.RemoteBook{ID: "p3"}}, Action: ActionAdoptAndAppend, HighlightsToAppend: 1},
					{Book: domain.Book{ID: "b4"}, Match: destination.BookMatch{Status: destination.BookMatchNone}, Action: ActionCreateAndAppend},
					{Book: domain.Book{ID: "b5"}, Match: destination.BookMatch{Status: destination.BookMatchAmbiguous}, Action: ActionSkip},
					{Book: domain.Book{ID: "b6"}, Match: destination.BookMatch{Status: destination.BookMatchAmbiguous}, Action: ActionSkip},
				},
				Summary: Summary{
					ExactMatches:       2,
					AdoptableMatches:   1,
					NewBooks:           1,
					Ambiguous:          2,
					HighlightsToAppend: 4,
				},
			},
			wantListCalls: []string{"b1", "b2", "b3", "b4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := &fakeDestination{matches: tt.matches}
			src := &fakeSource{highlights: tt.highlights}
			got, err := BuildPlan(context.Background(), tt.books, src, dst)
			if err != nil {
				t.Fatalf("BuildPlan() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("BuildPlan() = %+v, want %+v", got, tt.want)
			}
			wantFindCalls := make([]string, 0, len(tt.books))
			for _, b := range tt.books {
				wantFindCalls = append(wantFindCalls, b.ID)
			}
			if !slices.Equal(dst.calls, wantFindCalls) {
				t.Errorf("FindBook calls = %v, want %v", dst.calls, wantFindCalls)
			}
			if !slices.Equal(src.calls, tt.wantListCalls) {
				t.Errorf("ListHighlights calls = %v, want %v", src.calls, tt.wantListCalls)
			}
		})
	}
}

func TestBuildPlanError(t *testing.T) {
	wantErr := exitcode.New(exitcode.Notion, errors.New("notion: query failed"))

	tests := []struct {
		name          string
		dstErrs       map[string]error
		srcErrs       map[string]error
		wantFindCalls []string
		wantListCalls []string
	}{
		{
			name:          "FindBook が 2 冊目で失敗したら 3 冊目には行かない",
			dstErrs:       map[string]error{"b2": wantErr},
			wantFindCalls: []string{"b1", "b2"},
			wantListCalls: []string{"b1"},
		},
		{
			name:          "ListHighlights が 2 冊目で失敗したら 3 冊目には行かない",
			srcErrs:       map[string]error{"b2": wantErr},
			wantFindCalls: []string{"b1", "b2"},
			wantListCalls: []string{"b1", "b2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			books := []domain.Book{{ID: "b1"}, {ID: "b2"}, {ID: "b3"}}
			dst := &fakeDestination{
				matches: map[string]destination.BookMatch{
					"b1": {Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "p1"}},
					"b2": {Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "p2"}},
					"b3": {Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "p3"}},
				},
				errs: tt.dstErrs,
			}
			src := &fakeSource{
				highlights: map[string][]domain.Highlight{
					"b1": highlights("a-1"),
					"b2": highlights("a-2"),
					"b3": highlights("a-3"),
				},
				errs: tt.srcErrs,
			}
			got, err := BuildPlan(context.Background(), books, src, dst)
			if err != wantErr {
				t.Errorf("BuildPlan() error = %v, want %v", err, wantErr)
			}
			if exitcode.Code(err) != 4 {
				t.Errorf("exitcode.Code(err) = %d, want 4", exitcode.Code(err))
			}
			if !reflect.DeepEqual(got, Plan{}) {
				t.Errorf("BuildPlan() = %+v, want %+v", got, Plan{})
			}
			if !slices.Equal(dst.calls, tt.wantFindCalls) {
				t.Errorf("FindBook calls = %v, want %v", dst.calls, tt.wantFindCalls)
			}
			if !slices.Equal(src.calls, tt.wantListCalls) {
				t.Errorf("ListHighlights calls = %v, want %v", src.calls, tt.wantListCalls)
			}
		})
	}
}
