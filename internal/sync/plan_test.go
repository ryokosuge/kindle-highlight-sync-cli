package sync

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/destination"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
)

type fakeDestination struct {
	matches   map[string]destination.BookMatch
	errs      map[string]error
	remoteIDs map[string][]string // ページ ID ごとに ListHighlightIDs が返す Highlight ID
	listErrs  map[string]error    // ページ ID ごとに ListHighlightIDs が返す error
	calls     []string
	listCalls []string // ListHighlightIDs に渡ったページ ID の順
}

func (f *fakeDestination) FindBook(_ context.Context, book domain.Book) (destination.BookMatch, error) {
	f.calls = append(f.calls, book.ID)
	if err, ok := f.errs[book.ID]; ok {
		return destination.BookMatch{}, err
	}
	return f.matches[book.ID], nil
}

// CreateBook / AdoptBook / AppendHighlights は Destination を満たすためだけの stub。BuildPlan は書き込まないので呼ばれない。
func (f *fakeDestination) CreateBook(context.Context, domain.Book) (destination.RemoteBook, error) {
	return destination.RemoteBook{}, nil
}

func (f *fakeDestination) AdoptBook(context.Context, destination.RemoteBook, domain.Book) error {
	return nil
}

func (f *fakeDestination) AppendHighlights(context.Context, destination.RemoteBook, []domain.Highlight) ([]destination.RemoteHighlight, error) {
	return nil, nil
}

func (f *fakeDestination) ListHighlightIDs(_ context.Context, remote destination.RemoteBook) (map[string]destination.RemoteHighlight, error) {
	f.listCalls = append(f.listCalls, remote.ID)
	if err, ok := f.listErrs[remote.ID]; ok {
		return nil, err
	}
	known := make(map[string]destination.RemoteHighlight)
	for i, id := range f.remoteIDs[remote.ID] {
		known[id] = destination.RemoteHighlight{ID: fmt.Sprintf("block-%d", i), HighlightID: id}
	}
	return known, nil
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
		remoteIDs     map[string][]string
		want          Plan
		wantListCalls []string
		wantIDsCalls  []string // ListHighlightIDs を呼ぶのはページがある exact / adoptable だけ
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
			wantIDsCalls:  []string{"page-1"},
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
			wantIDsCalls:  []string{"page-9"},
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
			wantIDsCalls:  nil,
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
			wantIDsCalls:  nil,
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
			wantIDsCalls:  []string{"page-1"},
		},
		{
			name:  "exact の Book は Sync Marker で既知の Highlight を Append から除く",
			books: []domain.Book{{ID: "book-1", Title: "Go 本"}},
			matches: map[string]destination.BookMatch{
				"book-1": {Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "page-1"}},
			},
			highlights: map[string][]domain.Highlight{
				"book-1": highlights("a-1", "a-2", "a-3"),
			},
			remoteIDs: map[string][]string{"page-1": {"a-2"}},
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
			wantIDsCalls:  []string{"page-1"},
		},
		{
			name:  "adoptable の Book も Sync Marker で既知の Highlight を Append から除く",
			books: []domain.Book{{ID: "book-1", Title: "Go 本"}},
			matches: map[string]destination.BookMatch{
				"book-1": {Status: destination.BookMatchAdoptable, Book: &destination.RemoteBook{ID: "page-9"}},
			},
			highlights: map[string][]domain.Highlight{
				"book-1": highlights("a-1", "a-2"),
			},
			remoteIDs: map[string][]string{"page-9": {"a-1", "a-2"}},
			want: Plan{
				Books: []BookPlan{{
					Book:               domain.Book{ID: "book-1", Title: "Go 本"},
					Match:              destination.BookMatch{Status: destination.BookMatchAdoptable, Book: &destination.RemoteBook{ID: "page-9"}},
					Action:             ActionAdoptAndAppend,
					HighlightsToAppend: 0,
				}},
				Summary: Summary{AdoptableMatches: 1, HighlightsToAppend: 0},
			},
			wantListCalls: []string{"book-1"},
			wantIDsCalls:  []string{"page-9"},
		},
		{
			name:  "Notion にだけある Highlight ID は Append の数を負にせず数に影響しない",
			books: []domain.Book{{ID: "book-1", Title: "Go 本"}},
			matches: map[string]destination.BookMatch{
				"book-1": {Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "page-1"}},
			},
			highlights: map[string][]domain.Highlight{
				"book-1": highlights("a-1"),
			},
			remoteIDs: map[string][]string{"page-1": {"a-1", "a-8", "a-9"}},
			want: Plan{
				Books: []BookPlan{{
					Book:               domain.Book{ID: "book-1", Title: "Go 本"},
					Match:              destination.BookMatch{Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "page-1"}},
					Action:             ActionAppend,
					HighlightsToAppend: 0,
				}},
				Summary: Summary{ExactMatches: 1, HighlightsToAppend: 0},
			},
			wantListCalls: []string{"book-1"},
			wantIDsCalls:  []string{"page-1"},
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
			remoteIDs: map[string][]string{"p2": {"a-3"}},
			want: Plan{
				Books: []BookPlan{
					{Book: domain.Book{ID: "b1"}, Match: destination.BookMatch{Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "p1"}}, Action: ActionAppend, HighlightsToAppend: 1},
					{Book: domain.Book{ID: "b2"}, Match: destination.BookMatch{Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "p2"}}, Action: ActionAppend, HighlightsToAppend: 1},
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
					HighlightsToAppend: 3,
				},
			},
			wantListCalls: []string{"b1", "b2", "b3", "b4"},
			wantIDsCalls:  []string{"p1", "p2", "p3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := &fakeDestination{matches: tt.matches, remoteIDs: tt.remoteIDs}
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
			if !slices.Equal(dst.listCalls, tt.wantIDsCalls) {
				t.Errorf("ListHighlightIDs calls = %v, want %v", dst.listCalls, tt.wantIDsCalls)
			}
		})
	}
}

func TestBuildPlanError(t *testing.T) {
	wantErr := exitcode.New(exitcode.Notion, errors.New("notion: query failed"))

	tests := []struct {
		name          string
		dstErrs       map[string]error
		idsErrs       map[string]error
		srcErrs       map[string]error
		wantFindCalls []string
		wantIDsCalls  []string
		wantListCalls []string
	}{
		{
			name:          "FindBook が 2 冊目で失敗したら 3 冊目には行かない",
			dstErrs:       map[string]error{"b2": wantErr},
			wantFindCalls: []string{"b1", "b2"},
			wantIDsCalls:  []string{"p1"},
			wantListCalls: []string{"b1"},
		},
		{
			name:          "ListHighlightIDs が 2 冊目で失敗したら その Book の Highlight は取らず 3 冊目には行かない",
			idsErrs:       map[string]error{"p2": wantErr},
			wantFindCalls: []string{"b1", "b2"},
			wantIDsCalls:  []string{"p1", "p2"},
			wantListCalls: []string{"b1"},
		},
		{
			name:          "ListHighlights が 2 冊目で失敗したら 3 冊目には行かない",
			srcErrs:       map[string]error{"b2": wantErr},
			wantFindCalls: []string{"b1", "b2"},
			wantIDsCalls:  []string{"p1", "p2"},
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
				errs:     tt.dstErrs,
				listErrs: tt.idsErrs,
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
			if !slices.Equal(dst.listCalls, tt.wantIDsCalls) {
				t.Errorf("ListHighlightIDs calls = %v, want %v", dst.listCalls, tt.wantIDsCalls)
			}
			if !slices.Equal(src.calls, tt.wantListCalls) {
				t.Errorf("ListHighlights calls = %v, want %v", src.calls, tt.wantListCalls)
			}
		})
	}
}
