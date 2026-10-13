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
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/source"
)

type fakeDestination struct {
	matches           map[string]destination.BookMatch
	errs              map[string]error
	remoteIDs         map[string][]string                // ページ ID ごとに ListHighlightIDs が返す Highlight ID
	listErrs          map[string]error                   // ページ ID ごとに ListHighlightIDs が返す error
	writeErrs         map[string]error                   // Book ID ごとに CreateBook / AdoptBook / UpdateBook が返す error
	appendErrs        map[string]error                   // ページ ID ごとに AppendHighlights が返す error
	appendedBeforeErr map[string]int                     // ページ ID ごとに、appendErrs で失敗する前に追記できた件数
	refindMatches     map[string]destination.BookMatch   // Book ID ごとに RefindBook が返す結果
	refindErrs        map[string]error                   // Book ID ごとに RefindBook が返す error
	refindSeq         map[string][]destination.BookMatch // Book ID ごとに RefindBook が呼ばれた順に返す結果。尽きたら最後を返す。あれば refindMatches より優先
	createErrSeq      map[string][]error                 // Book ID ごとに CreateBook が呼ばれた順に返す error。nil は成功。尽きたら成功。あれば writeErrs より優先
	refindCalls       []string                           // RefindBook に渡った Book ID の順
	calls             []string
	appends           map[string][]string // ページ ID ごとに AppendHighlights に渡った Highlight ID の順
	listCalls         []string            // ListHighlightIDs に渡ったページ ID の順
	writes            []string            // 呼ばれた CreateBook / AdoptBook / UpdateBook の順（"create <Book ID>" / "adopt <ページ ID> <Book ID>" / "update <ページ ID> <Book ID>"）
}

// countOf は s に v が現れる回数。
func countOf(s []string, v string) int {
	n := 0
	for _, e := range s {
		if e == v {
			n++
		}
	}
	return n
}

func (f *fakeDestination) FindBook(_ context.Context, book domain.Book) (destination.BookMatch, error) {
	f.calls = append(f.calls, book.ID)
	if err, ok := f.errs[book.ID]; ok {
		return destination.BookMatch{}, err
	}
	return f.matches[book.ID], nil
}

func (f *fakeDestination) RefindBook(_ context.Context, book domain.Book) (destination.BookMatch, error) {
	f.refindCalls = append(f.refindCalls, book.ID)
	if err, ok := f.refindErrs[book.ID]; ok {
		return destination.BookMatch{}, err
	}
	if seq := f.refindSeq[book.ID]; len(seq) > 0 {
		return seq[min(countOf(f.refindCalls, book.ID), len(seq))-1], nil
	}
	return f.refindMatches[book.ID], nil
}

// CreateBook は呼ばれたことを writes に記録し、Book ID から決まる ID のページを返す。BuildPlan は書き込まないので呼ばれない。
func (f *fakeDestination) CreateBook(_ context.Context, book domain.Book) (destination.RemoteBook, error) {
	f.writes = append(f.writes, "create "+book.ID)
	if seq, ok := f.createErrSeq[book.ID]; ok {
		if n := countOf(f.writes, "create "+book.ID); n <= len(seq) && seq[n-1] != nil {
			return destination.RemoteBook{}, seq[n-1]
		}
		return destination.RemoteBook{ID: "page-" + book.ID}, nil
	}
	if err, ok := f.writeErrs[book.ID]; ok {
		return destination.RemoteBook{}, err
	}
	return destination.RemoteBook{ID: "page-" + book.ID}, nil
}

func (f *fakeDestination) AdoptBook(_ context.Context, remote destination.RemoteBook, book domain.Book) error {
	f.writes = append(f.writes, "adopt "+remote.ID+" "+book.ID)
	if err, ok := f.writeErrs[book.ID]; ok {
		return err
	}
	return nil
}

// AppendHighlights は渡された Highlight ID を appends に記録し、appendErrs があれば appendedBeforeErr の件数だけ成功した分と error を返す。
func (f *fakeDestination) AppendHighlights(_ context.Context, remote destination.RemoteBook, hs []domain.Highlight) ([]destination.RemoteHighlight, error) {
	if f.appends == nil {
		f.appends = make(map[string][]string)
	}
	for _, h := range hs {
		f.appends[remote.ID] = append(f.appends[remote.ID], h.ID)
	}
	err := f.appendErrs[remote.ID]
	if err != nil {
		hs = hs[:f.appendedBeforeErr[remote.ID]]
	}
	appended := make([]destination.RemoteHighlight, 0, len(hs))
	for _, h := range hs {
		appended = append(appended, destination.RemoteHighlight{ID: "block-" + h.ID, HighlightID: h.ID})
	}
	return appended, err
}

func (f *fakeDestination) UpdateBook(_ context.Context, remote destination.RemoteBook, book domain.Book) error {
	f.writes = append(f.writes, "update "+remote.ID+" "+book.ID)
	if err, ok := f.writeErrs[book.ID]; ok {
		return err
	}
	return nil
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
			books: []domain.Book{{ID: "book-1", Title: "Go 本", ASIN: "ASIN-book-1"}},
			matches: map[string]destination.BookMatch{
				"book-1": {Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "page-1"}},
			},
			highlights: map[string][]domain.Highlight{
				"book-1": highlights("a-1", "a-2"),
			},
			want: Plan{
				Books: []BookPlan{{
					Book:               domain.Book{ID: "book-1", Title: "Go 本", ASIN: "ASIN-book-1"},
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
			books: []domain.Book{{ID: "book-1", Title: "Go 本", ASIN: "ASIN-book-1"}},
			matches: map[string]destination.BookMatch{
				"book-1": {Status: destination.BookMatchAdoptable, Book: &destination.RemoteBook{ID: "page-9"}},
			},
			highlights: map[string][]domain.Highlight{
				"book-1": highlights("a-1"),
			},
			want: Plan{
				Books: []BookPlan{{
					Book:               domain.Book{ID: "book-1", Title: "Go 本", ASIN: "ASIN-book-1"},
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
			books: []domain.Book{{ID: "book-1", Title: "Go 本", ASIN: "ASIN-book-1"}},
			matches: map[string]destination.BookMatch{
				"book-1": {Status: destination.BookMatchNone},
			},
			highlights: map[string][]domain.Highlight{
				"book-1": highlights("a-1", "a-2", "a-3"),
			},
			want: Plan{
				Books: []BookPlan{{
					Book:               domain.Book{ID: "book-1", Title: "Go 本", ASIN: "ASIN-book-1"},
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
			books: []domain.Book{{ID: "book-1", Title: "Go 本", ASIN: "ASIN-book-1"}},
			matches: map[string]destination.BookMatch{
				"book-1": {Status: destination.BookMatchAmbiguous},
			},
			highlights: map[string][]domain.Highlight{
				"book-1": highlights("a-1", "a-2"),
			},
			want: Plan{
				Books: []BookPlan{{
					Book:   domain.Book{ID: "book-1", Title: "Go 本", ASIN: "ASIN-book-1"},
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
			books: []domain.Book{{ID: "book-1", Title: "Go 本", ASIN: "ASIN-book-1"}},
			matches: map[string]destination.BookMatch{
				"book-1": {Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "page-1"}},
			},
			highlights: map[string][]domain.Highlight{
				"book-1": {{ID: "a-1", Note: "メモ"}},
			},
			want: Plan{
				Books: []BookPlan{{
					Book:               domain.Book{ID: "book-1", Title: "Go 本", ASIN: "ASIN-book-1"},
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
			books: []domain.Book{{ID: "book-1", Title: "Go 本", ASIN: "ASIN-book-1"}},
			matches: map[string]destination.BookMatch{
				"book-1": {Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "page-1"}},
			},
			highlights: map[string][]domain.Highlight{
				"book-1": highlights("a-1", "a-2", "a-3"),
			},
			remoteIDs: map[string][]string{"page-1": {"a-2"}},
			want: Plan{
				Books: []BookPlan{{
					Book:               domain.Book{ID: "book-1", Title: "Go 本", ASIN: "ASIN-book-1"},
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
			books: []domain.Book{{ID: "book-1", Title: "Go 本", ASIN: "ASIN-book-1"}},
			matches: map[string]destination.BookMatch{
				"book-1": {Status: destination.BookMatchAdoptable, Book: &destination.RemoteBook{ID: "page-9"}},
			},
			highlights: map[string][]domain.Highlight{
				"book-1": highlights("a-1", "a-2"),
			},
			remoteIDs: map[string][]string{"page-9": {"a-1", "a-2"}},
			want: Plan{
				Books: []BookPlan{{
					Book:               domain.Book{ID: "book-1", Title: "Go 本", ASIN: "ASIN-book-1"},
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
			books: []domain.Book{{ID: "book-1", Title: "Go 本", ASIN: "ASIN-book-1"}},
			matches: map[string]destination.BookMatch{
				"book-1": {Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "page-1"}},
			},
			highlights: map[string][]domain.Highlight{
				"book-1": highlights("a-1"),
			},
			remoteIDs: map[string][]string{"page-1": {"a-1", "a-8", "a-9"}},
			want: Plan{
				Books: []BookPlan{{
					Book:               domain.Book{ID: "book-1", Title: "Go 本", ASIN: "ASIN-book-1"},
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
				{ID: "b1", ASIN: "ASIN-b1"},
				{ID: "b2", ASIN: "ASIN-b2"},
				{ID: "b3", ASIN: "ASIN-b3"},
				{ID: "b4", ASIN: "ASIN-b4"},
				{ID: "b5", ASIN: "ASIN-b5"},
				{ID: "b6", ASIN: "ASIN-b6"},
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
					{Book: domain.Book{ID: "b1", ASIN: "ASIN-b1"}, Match: destination.BookMatch{Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "p1"}}, Action: ActionAppend, HighlightsToAppend: 1},
					{Book: domain.Book{ID: "b2", ASIN: "ASIN-b2"}, Match: destination.BookMatch{Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "p2"}}, Action: ActionAppend, HighlightsToAppend: 1},
					{Book: domain.Book{ID: "b3", ASIN: "ASIN-b3"}, Match: destination.BookMatch{Status: destination.BookMatchAdoptable, Book: &destination.RemoteBook{ID: "p3"}}, Action: ActionAdoptAndAppend, HighlightsToAppend: 1},
					{Book: domain.Book{ID: "b4", ASIN: "ASIN-b4"}, Match: destination.BookMatch{Status: destination.BookMatchNone}, Action: ActionCreateAndAppend},
					{Book: domain.Book{ID: "b5", ASIN: "ASIN-b5"}, Match: destination.BookMatch{Status: destination.BookMatchAmbiguous}, Action: ActionSkip},
					{Book: domain.Book{ID: "b6", ASIN: "ASIN-b6"}, Match: destination.BookMatch{Status: destination.BookMatchAmbiguous}, Action: ActionSkip},
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
	notionErr := exitcode.New(exitcode.Notion, errors.New("notion: query failed"))
	localStateErr := exitcode.New(exitcode.LocalState, errors.New("destination: highlight ID scheme mismatch"))

	tests := []struct {
		name          string
		wantErr       error
		wantCode      int
		dstErrs       map[string]error
		idsErrs       map[string]error
		srcErrs       map[string]error
		wantFindCalls []string
		wantIDsCalls  []string
		wantListCalls []string
	}{
		{
			name:          "FindBook が 2 冊目で失敗したら 3 冊目には行かない",
			wantErr:       notionErr,
			wantCode:      4,
			dstErrs:       map[string]error{"b2": notionErr},
			wantFindCalls: []string{"b1", "b2"},
			wantIDsCalls:  []string{"p1"},
			wantListCalls: []string{"b1"},
		},
		{
			name:          "ListHighlightIDs が 2 冊目で失敗したら その Book の Highlight は取らず 3 冊目には行かない",
			wantErr:       notionErr,
			wantCode:      4,
			idsErrs:       map[string]error{"p2": notionErr},
			wantFindCalls: []string{"b1", "b2"},
			wantIDsCalls:  []string{"p1", "p2"},
			wantListCalls: []string{"b1"},
		},
		{
			name:          "ListHighlightIDs が LocalState の error なら Failed に残さずその error で 3 冊目には行かない",
			wantErr:       localStateErr,
			wantCode:      6,
			idsErrs:       map[string]error{"p2": localStateErr},
			wantFindCalls: []string{"b1", "b2"},
			wantIDsCalls:  []string{"p1", "p2"},
			wantListCalls: []string{"b1"},
		},
		{
			name:          "ListHighlights が 2 冊目で失敗したら 3 冊目には行かない",
			wantErr:       notionErr,
			wantCode:      4,
			srcErrs:       map[string]error{"b2": notionErr},
			wantFindCalls: []string{"b1", "b2"},
			wantIDsCalls:  []string{"p1", "p2"},
			wantListCalls: []string{"b1", "b2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			books := []domain.Book{{ID: "b1", ASIN: "ASIN-b1"}, {ID: "b2", ASIN: "ASIN-b2"}, {ID: "b3", ASIN: "ASIN-b3"}}
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
			if err != tt.wantErr {
				t.Errorf("BuildPlan() error = %v, want %v", err, tt.wantErr)
			}
			if exitcode.Code(err) != tt.wantCode {
				t.Errorf("exitcode.Code(err) = %d, want %d", exitcode.Code(err), tt.wantCode)
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

func TestBuildPlanBookFailures(t *testing.T) {
	exact := func(book, page string) destination.BookMatch {
		return destination.BookMatch{Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: page}}
	}

	tests := []struct {
		name          string
		books         []domain.Book
		srcErrs       map[string]error
		want          Plan
		wantFindCalls []string
		wantIDsCalls  []string
		wantListCalls []string
	}{
		{
			name:  "ASIN の無い Book は照合も取得もせず失敗に残り予定と summary に入らない",
			books: []domain.Book{{ID: "b1", ASIN: "ASIN-b1"}, {ID: "b-noasin"}, {ID: "b3", ASIN: "ASIN-b3"}},
			want: Plan{
				Books: []BookPlan{
					{Book: domain.Book{ID: "b1", ASIN: "ASIN-b1"}, Match: exact("b1", "p1"), Action: ActionAppend, HighlightsToAppend: 1},
					{Book: domain.Book{ID: "b3", ASIN: "ASIN-b3"}, Match: exact("b3", "p3"), Action: ActionAppend, HighlightsToAppend: 1},
				},
				Summary: Summary{ExactMatches: 2, HighlightsToAppend: 2},
				Failed:  []FailedBook{{BookID: "b-noasin", Kind: FailureNoASIN}},
			},
			wantFindCalls: []string{"b1", "b3"},
			wantIDsCalls:  []string{"p1", "p3"},
			wantListCalls: []string{"b1", "b3"},
		},
		{
			name:    "2 冊目の構造の異常は失敗に残り 3 冊目の予定が組まれる",
			books:   []domain.Book{{ID: "b1", ASIN: "ASIN-b1"}, {ID: "b2", ASIN: "ASIN-b2"}, {ID: "b3", ASIN: "ASIN-b3"}},
			srcErrs: map[string]error{"b2": fmt.Errorf("source: %w", source.ErrSourceFormat)},
			want: Plan{
				Books: []BookPlan{
					{Book: domain.Book{ID: "b1", ASIN: "ASIN-b1"}, Match: exact("b1", "p1"), Action: ActionAppend, HighlightsToAppend: 1},
					{Book: domain.Book{ID: "b3", ASIN: "ASIN-b3"}, Match: exact("b3", "p3"), Action: ActionAppend, HighlightsToAppend: 1},
				},
				Summary: Summary{ExactMatches: 2, HighlightsToAppend: 2},
				Failed:  []FailedBook{{BookID: "b2", Kind: FailureNotebookStructure}},
			},
			wantFindCalls: []string{"b1", "b2", "b3"},
			wantIDsCalls:  []string{"p1", "p2", "p3"},
			wantListCalls: []string{"b1", "b2", "b3"},
		},
		{
			name:    "2 冊目の Highlight ID が作れない error は失敗に残り 3 冊目の予定が組まれる",
			books:   []domain.Book{{ID: "b1", ASIN: "ASIN-b1"}, {ID: "b2", ASIN: "ASIN-b2"}, {ID: "b3", ASIN: "ASIN-b3"}},
			srcErrs: map[string]error{"b2": fmt.Errorf("source: %w: %w", domain.ErrMissingAnnotationID, source.ErrSourceFormat)},
			want: Plan{
				Books: []BookPlan{
					{Book: domain.Book{ID: "b1", ASIN: "ASIN-b1"}, Match: exact("b1", "p1"), Action: ActionAppend, HighlightsToAppend: 1},
					{Book: domain.Book{ID: "b3", ASIN: "ASIN-b3"}, Match: exact("b3", "p3"), Action: ActionAppend, HighlightsToAppend: 1},
				},
				Summary: Summary{ExactMatches: 2, HighlightsToAppend: 2},
				Failed:  []FailedBook{{BookID: "b2", Kind: FailureHighlightID}},
			},
			wantFindCalls: []string{"b1", "b2", "b3"},
			wantIDsCalls:  []string{"p1", "p2", "p3"},
			wantListCalls: []string{"b1", "b2", "b3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := &fakeDestination{matches: map[string]destination.BookMatch{
				"b1": exact("b1", "p1"),
				"b2": exact("b2", "p2"),
				"b3": exact("b3", "p3"),
			}}
			src := &fakeSource{
				highlights: map[string][]domain.Highlight{
					"b1": highlights("a-1"),
					"b2": highlights("a-2"),
					"b3": highlights("a-3"),
				},
				errs: tt.srcErrs,
			}
			got, err := BuildPlan(context.Background(), tt.books, src, dst)
			if err != nil {
				t.Fatalf("BuildPlan() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("BuildPlan() = %+v, want %+v", got, tt.want)
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

func TestBuildPlanFetchStops(t *testing.T) {
	tests := []struct {
		name   string
		srcErr error
	}{
		{
			name:   "枠の異常は止まる",
			srcErr: fmt.Errorf("source: %w: %w", source.ErrHighlightsFrame, source.ErrSourceFormat),
		},
		{
			name:   "ctx の取消は止まる",
			srcErr: fmt.Errorf("source: %w", context.Canceled),
		},
		{
			name:   "期限切れは止まる",
			srcErr: fmt.Errorf("source: %w", context.DeadlineExceeded),
		},
		{
			name:   "ブラウザ由来の失敗は止まる",
			srcErr: exitcode.New(exitcode.Browser, errors.New("browser: crashed")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			books := []domain.Book{{ID: "b1", ASIN: "ASIN-b1"}, {ID: "b2", ASIN: "ASIN-b2"}, {ID: "b3", ASIN: "ASIN-b3"}}
			dst := &fakeDestination{matches: map[string]destination.BookMatch{
				"b1": {Status: destination.BookMatchNone},
				"b2": {Status: destination.BookMatchNone},
				"b3": {Status: destination.BookMatchNone},
			}}
			src := &fakeSource{errs: map[string]error{"b2": tt.srcErr}}
			got, err := BuildPlan(context.Background(), books, src, dst)
			if err != tt.srcErr {
				t.Errorf("BuildPlan() error = %v, want %v", err, tt.srcErr)
			}
			if !reflect.DeepEqual(got, Plan{}) {
				t.Errorf("BuildPlan() = %+v, want %+v", got, Plan{})
			}
			if want := []string{"b1", "b2"}; !slices.Equal(src.calls, want) {
				t.Errorf("ListHighlights calls = %v, want %v", src.calls, want)
			}
		})
	}
}
