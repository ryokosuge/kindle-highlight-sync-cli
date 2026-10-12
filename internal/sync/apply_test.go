package sync

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/destination"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/notion"
)

// applyBooks は照合の結果が none / adoptable / exact / ambiguous の順に並ぶ 4 冊。タイトルは文言に出ないことを確かめるために入れる。
var applyBooks = []domain.Book{
	{ID: "book-new", Title: "New Book"},
	{ID: "book-adopt", Title: "Adopt Book"},
	{ID: "book-exact", Title: "Exact Book"},
	{ID: "book-ambiguous", Title: "Ambiguous Book"},
}

var applyMatches = map[string]destination.BookMatch{
	"book-new":       {Status: destination.BookMatchNone},
	"book-adopt":     {Status: destination.BookMatchAdoptable, Book: &destination.RemoteBook{ID: "page-adopt"}},
	"book-exact":     {Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "page-exact"}},
	"book-ambiguous": {Status: destination.BookMatchAmbiguous},
}

// notionStatusError は Notion client が HTTP status の失敗に返す error の形（exitcode.Notion で包んだ StatusError）。
func notionStatusError(status int) error {
	return exitcode.New(exitcode.Notion, &notion.StatusError{Status: status, Code: http.StatusText(status)})
}

// notionTimeoutError は Notion client がタイムアウトに返す error の形（status は無い）。
var notionTimeoutError = exitcode.New(exitcode.Notion, fmt.Errorf("notion: POST /v1/pages: %w", context.DeadlineExceeded))

// notionCanceledError は Notion client が context のキャンセルに返す error の形。
var notionCanceledError = exitcode.New(exitcode.Notion, fmt.Errorf("notion: POST /v1/pages: %w", context.Canceled))

func TestApplyBooks(t *testing.T) {
	tests := []struct {
		name       string
		books      []domain.Book
		matches    map[string]destination.BookMatch
		findErrs   map[string]error
		writeErrs  map[string]error
		want       ApplyResult
		wantWrites []string
		wantCode   int    // ApplyBooks が返す error の exit code（0 なら nil）
		wantErr    string // ApplyBooks が返す error の文言
		wantSame   error  // nil 以外なら、ApplyBooks が返す error がこの値そのもの（包んでいない）であることを確かめる
	}{
		{
			name:    "none は CreateBook、adoptable は AdoptBook、exact は UpdateBook を呼び、ambiguous は skip する",
			books:   applyBooks,
			matches: applyMatches,
			want: ApplyResult{
				Created: []string{"book-new"},
				Adopted: []string{"book-adopt"},
				Exact:   []string{"book-exact"},
				Skipped: []string{"book-ambiguous"},
			},
			wantWrites: []string{"create book-new", "adopt page-adopt book-adopt", "update page-exact book-exact"},
		},
		{
			name:    "Book が無ければ何も呼ばず空の結果",
			books:   nil,
			matches: applyMatches,
			want:    ApplyResult{},
		},
		{
			name:      "CreateBook が 400 で失敗しても残りの Book を続け、失敗した Book ID が残る",
			books:     applyBooks,
			matches:   applyMatches,
			writeErrs: map[string]error{"book-new": notionStatusError(http.StatusBadRequest)},
			want: ApplyResult{
				Adopted: []string{"book-adopt"},
				Exact:   []string{"book-exact"},
				Skipped: []string{"book-ambiguous"},
				Failed:  []BookFailure{{BookID: "book-new", Kind: BookFailureCreate}},
			},
			wantWrites: []string{"create book-new", "adopt page-adopt book-adopt", "update page-exact book-exact"},
		},
		{
			name:      "AdoptBook が 5xx で失敗しても残りの Book を続け、失敗した Book ID が残る",
			books:     applyBooks,
			matches:   applyMatches,
			writeErrs: map[string]error{"book-adopt": notionStatusError(http.StatusBadGateway)},
			want: ApplyResult{
				Created: []string{"book-new"},
				Exact:   []string{"book-exact"},
				Skipped: []string{"book-ambiguous"},
				Failed:  []BookFailure{{BookID: "book-adopt", Kind: BookFailureAdopt}},
			},
			wantWrites: []string{"create book-new", "adopt page-adopt book-adopt", "update page-exact book-exact"},
		},
		{
			name:      "CreateBook がタイムアウトしても再送せず、残りの Book を続ける",
			books:     applyBooks,
			matches:   applyMatches,
			writeErrs: map[string]error{"book-new": notionTimeoutError},
			want: ApplyResult{
				Adopted: []string{"book-adopt"},
				Exact:   []string{"book-exact"},
				Skipped: []string{"book-ambiguous"},
				Failed:  []BookFailure{{BookID: "book-new", Kind: BookFailureCreate}},
			},
			wantWrites: []string{"create book-new", "adopt page-adopt book-adopt", "update page-exact book-exact"},
		},
		{
			name:       "CreateBook が context のキャンセルで失敗したら残りの Book を書かずに元の error をそのまま返す",
			books:      applyBooks,
			matches:    applyMatches,
			writeErrs:  map[string]error{"book-new": notionCanceledError},
			want:       ApplyResult{},
			wantWrites: []string{"create book-new"},
			wantCode:   4,
			wantErr:    "notion: POST /v1/pages: context canceled",
			wantSame:   notionCanceledError,
		},
		{
			name:       "CreateBook が 401 で失敗したら残りの Book を書かずに exitcode.Notion の error を返す",
			books:      applyBooks,
			matches:    applyMatches,
			writeErrs:  map[string]error{"book-new": notionStatusError(http.StatusUnauthorized)},
			want:       ApplyResult{},
			wantWrites: []string{"create book-new"},
			wantCode:   4,
			wantErr:    "sync: create failed book-new: HTTP 401 Unauthorized",
		},
		{
			name:       "AdoptBook が 403 で失敗したら残りの Book を書かずに exitcode.Notion の error を返す",
			books:      applyBooks,
			matches:    applyMatches,
			writeErrs:  map[string]error{"book-adopt": notionStatusError(http.StatusForbidden)},
			want:       ApplyResult{Created: []string{"book-new"}},
			wantWrites: []string{"create book-new", "adopt page-adopt book-adopt"},
			wantCode:   4,
			wantErr:    "sync: adopt failed book-adopt: HTTP 403 Forbidden",
		},
		{
			name:       "AdoptBook が 404 で失敗したら残りの Book を書かずに exitcode.Notion の error を返す",
			books:      applyBooks,
			matches:    applyMatches,
			writeErrs:  map[string]error{"book-adopt": notionStatusError(http.StatusNotFound)},
			want:       ApplyResult{Created: []string{"book-new"}},
			wantWrites: []string{"create book-new", "adopt page-adopt book-adopt"},
			wantCode:   4,
			wantErr:    "sync: adopt failed book-adopt: HTTP 404 Not Found",
		},
		{
			name:      "UpdateBook が 400 で失敗しても残りの Book を続け、失敗した Book ID が残り、exact には数えない",
			books:     applyBooks,
			matches:   applyMatches,
			writeErrs: map[string]error{"book-exact": notionStatusError(http.StatusBadRequest)},
			want: ApplyResult{
				Created: []string{"book-new"},
				Adopted: []string{"book-adopt"},
				Skipped: []string{"book-ambiguous"},
				Failed:  []BookFailure{{BookID: "book-exact", Kind: BookFailureUpdate}},
			},
			wantWrites: []string{"create book-new", "adopt page-adopt book-adopt", "update page-exact book-exact"},
		},
		{
			name:      "UpdateBook が 5xx で失敗しても残りの Book を続け、失敗した Book ID が残る",
			books:     applyBooks,
			matches:   applyMatches,
			writeErrs: map[string]error{"book-exact": notionStatusError(http.StatusBadGateway)},
			want: ApplyResult{
				Created: []string{"book-new"},
				Adopted: []string{"book-adopt"},
				Skipped: []string{"book-ambiguous"},
				Failed:  []BookFailure{{BookID: "book-exact", Kind: BookFailureUpdate}},
			},
			wantWrites: []string{"create book-new", "adopt page-adopt book-adopt", "update page-exact book-exact"},
		},
		{
			name:      "UpdateBook がタイムアウトしても残りの Book を続け、失敗した Book ID が残る",
			books:     applyBooks,
			matches:   applyMatches,
			writeErrs: map[string]error{"book-exact": notionTimeoutError},
			want: ApplyResult{
				Created: []string{"book-new"},
				Adopted: []string{"book-adopt"},
				Skipped: []string{"book-ambiguous"},
				Failed:  []BookFailure{{BookID: "book-exact", Kind: BookFailureUpdate}},
			},
			wantWrites: []string{"create book-new", "adopt page-adopt book-adopt", "update page-exact book-exact"},
		},
		{
			name:       "UpdateBook が 401 で失敗したら残りの Book を書かずに exitcode.Notion の error を返す",
			books:      applyBooks,
			matches:    applyMatches,
			writeErrs:  map[string]error{"book-exact": notionStatusError(http.StatusUnauthorized)},
			want:       ApplyResult{Created: []string{"book-new"}, Adopted: []string{"book-adopt"}},
			wantWrites: []string{"create book-new", "adopt page-adopt book-adopt", "update page-exact book-exact"},
			wantCode:   4,
			wantErr:    "sync: update failed book-exact: HTTP 401 Unauthorized",
		},
		{
			name:       "UpdateBook が 403 で失敗したら残りの Book を書かずに exitcode.Notion の error を返す",
			books:      applyBooks,
			matches:    applyMatches,
			writeErrs:  map[string]error{"book-exact": notionStatusError(http.StatusForbidden)},
			want:       ApplyResult{Created: []string{"book-new"}, Adopted: []string{"book-adopt"}},
			wantWrites: []string{"create book-new", "adopt page-adopt book-adopt", "update page-exact book-exact"},
			wantCode:   4,
			wantErr:    "sync: update failed book-exact: HTTP 403 Forbidden",
		},
		{
			name:       "UpdateBook が 404 で失敗したら残りの Book を書かずに exitcode.Notion の error を返す",
			books:      applyBooks,
			matches:    applyMatches,
			writeErrs:  map[string]error{"book-exact": notionStatusError(http.StatusNotFound)},
			want:       ApplyResult{Created: []string{"book-new"}, Adopted: []string{"book-adopt"}},
			wantWrites: []string{"create book-new", "adopt page-adopt book-adopt", "update page-exact book-exact"},
			wantCode:   4,
			wantErr:    "sync: update failed book-exact: HTTP 404 Not Found",
		},
		{
			name:       "FindBook が失敗したらその時点で exitcode.Notion の error を返す",
			books:      applyBooks,
			matches:    applyMatches,
			findErrs:   map[string]error{"book-adopt": exitcode.New(exitcode.Notion, errors.New("notion: data source query failed"))},
			want:       ApplyResult{Created: []string{"book-new"}},
			wantWrites: []string{"create book-new"},
			wantCode:   4,
			wantErr:    "sync: find book book-adopt: notion: data source query failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := &fakeDestination{matches: tt.matches, errs: tt.findErrs, writeErrs: tt.writeErrs}

			got, err := ApplyBooks(context.Background(), tt.books, &fakeSource{}, dst)
			if code := exitcode.Code(err); code != tt.wantCode {
				t.Fatalf("exitcode.Code(err) = %d, want %d (err = %v)", code, tt.wantCode, err)
			}
			if err != nil && err.Error() != tt.wantErr {
				t.Errorf("err = %q, want %q", err.Error(), tt.wantErr)
			}
			if tt.wantSame != nil && err != tt.wantSame {
				t.Errorf("err = %v (%T), want the same error %v", err, err, tt.wantSame)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ApplyBooks() = %+v, want %+v", got, tt.want)
			}
			if !slices.Equal(dst.writes, tt.wantWrites) {
				t.Errorf("writes = %v, want %v", dst.writes, tt.wantWrites)
			}
		})
	}
}

func applyHighlights(ids ...string) []domain.Highlight {
	hs := make([]domain.Highlight, 0, len(ids))
	for _, id := range ids {
		hs = append(hs, domain.Highlight{ID: id})
	}
	return hs
}

func TestApplyBooksAppend(t *testing.T) {
	// 4 冊とも Kindle の Highlight は a-1, a-2, a-3（この順）
	allThree := map[string][]domain.Highlight{}
	for _, b := range applyBooks {
		allThree[b.ID] = applyHighlights("a-1", "a-2", "a-3")
	}
	tests := []struct {
		name              string
		noHighlights      bool
		remoteIDs         map[string][]string
		listErrs          map[string]error
		srcErrs           map[string]error
		appendErrs        map[string]error
		appendedBeforeErr map[string]int
		want              ApplyResult
		wantAppends       map[string][]string
		wantListCalls     []string
		wantCode          int
		wantErr           string
	}{
		{
			name:      "exact / adoptable は既知を除いた Highlight を Kindle の順のまま渡し、作成した Book は一覧を取らず全部渡し、ambiguous は呼ばない",
			remoteIDs: map[string][]string{"page-adopt": {"a-2"}, "page-exact": {"a-3", "a-1"}},
			want: ApplyResult{
				Created:  []string{"book-new"},
				Adopted:  []string{"book-adopt"},
				Exact:    []string{"book-exact"},
				Skipped:  []string{"book-ambiguous"},
				Appended: 3 + 2 + 1,
			},
			wantAppends:   map[string][]string{"page-book-new": {"a-1", "a-2", "a-3"}, "page-adopt": {"a-1", "a-3"}, "page-exact": {"a-2"}},
			wantListCalls: []string{"page-adopt", "page-exact"},
		},
		{
			name:         "Highlight が 0 件の Book は Notion の一覧を読まず AppendHighlights も呼ばない",
			noHighlights: true,
			want: ApplyResult{
				Created: []string{"book-new"},
				Adopted: []string{"book-adopt"},
				Exact:   []string{"book-exact"},
				Skipped: []string{"book-ambiguous"},
			},
		},
		{
			name:      "追記するものが無い Book では AppendHighlights を呼ばない",
			remoteIDs: map[string][]string{"page-adopt": {"a-1", "a-2", "a-3", "a-9"}, "page-exact": {"a-1", "a-2", "a-3"}},
			want: ApplyResult{
				Created:  []string{"book-new"},
				Adopted:  []string{"book-adopt"},
				Exact:    []string{"book-exact"},
				Skipped:  []string{"book-ambiguous"},
				Appended: 3,
			},
			wantAppends:   map[string][]string{"page-book-new": {"a-1", "a-2", "a-3"}},
			wantListCalls: []string{"page-adopt", "page-exact"},
		},
		{
			name:              "追記が 429 で失敗しても残りの Book を続け、成功した分は数えて Book ID が失敗に残る",
			appendErrs:        map[string]error{"page-adopt": notionStatusError(http.StatusTooManyRequests)},
			appendedBeforeErr: map[string]int{"page-adopt": 1},
			want: ApplyResult{
				Created:  []string{"book-new"},
				Adopted:  []string{"book-adopt"},
				Exact:    []string{"book-exact"},
				Skipped:  []string{"book-ambiguous"},
				Failed:   []BookFailure{{BookID: "book-adopt", Kind: BookFailureAppend}},
				Appended: 3 + 1 + 3,
			},
			wantAppends:   map[string][]string{"page-book-new": {"a-1", "a-2", "a-3"}, "page-adopt": {"a-1", "a-2", "a-3"}, "page-exact": {"a-1", "a-2", "a-3"}},
			wantListCalls: []string{"page-adopt", "page-exact"},
		},
		{
			name:       "追記がタイムアウトしても残りの Book を続け、作成した Book は作成済みのまま失敗に残る",
			appendErrs: map[string]error{"page-book-new": notionTimeoutError},
			want: ApplyResult{
				Created:  []string{"book-new"},
				Adopted:  []string{"book-adopt"},
				Exact:    []string{"book-exact"},
				Skipped:  []string{"book-ambiguous"},
				Failed:   []BookFailure{{BookID: "book-new", Kind: BookFailureAppend}},
				Appended: 6,
			},
			wantAppends:   map[string][]string{"page-book-new": {"a-1", "a-2", "a-3"}, "page-adopt": {"a-1", "a-2", "a-3"}, "page-exact": {"a-1", "a-2", "a-3"}},
			wantListCalls: []string{"page-adopt", "page-exact"},
		},
		{
			name:     "既知の取得が 5xx で失敗しても残りの Book を続け、その Book には追記しない",
			listErrs: map[string]error{"page-adopt": notionStatusError(http.StatusBadGateway)},
			want: ApplyResult{
				Created:  []string{"book-new"},
				Adopted:  []string{"book-adopt"},
				Exact:    []string{"book-exact"},
				Skipped:  []string{"book-ambiguous"},
				Failed:   []BookFailure{{BookID: "book-adopt", Kind: BookFailureListKnown}},
				Appended: 6,
			},
			wantAppends:   map[string][]string{"page-book-new": {"a-1", "a-2", "a-3"}, "page-exact": {"a-1", "a-2", "a-3"}},
			wantListCalls: []string{"page-adopt", "page-exact"},
		},
		{
			name:    "Highlight の取得が失敗しても残りの Book を続け、その Book には追記しない",
			srcErrs: map[string]error{"book-exact": errors.New("source: timeout")},
			want: ApplyResult{
				Created:  []string{"book-new"},
				Adopted:  []string{"book-adopt"},
				Exact:    []string{"book-exact"},
				Skipped:  []string{"book-ambiguous"},
				Failed:   []BookFailure{{BookID: "book-exact", Kind: BookFailureFetch}},
				Appended: 6,
			},
			wantAppends:   map[string][]string{"page-book-new": {"a-1", "a-2", "a-3"}, "page-adopt": {"a-1", "a-2", "a-3"}},
			wantListCalls: []string{"page-adopt"},
		},
		{
			name:       "追記が 401 で失敗したら残りの Book を書かずに exitcode.Notion の error を返し、それまでの件数は残る",
			appendErrs: map[string]error{"page-adopt": notionStatusError(http.StatusUnauthorized)},
			want: ApplyResult{
				Created:  []string{"book-new"},
				Adopted:  []string{"book-adopt"},
				Appended: 3,
			},
			wantAppends:   map[string][]string{"page-book-new": {"a-1", "a-2", "a-3"}, "page-adopt": {"a-1", "a-2", "a-3"}},
			wantListCalls: []string{"page-adopt"},
			wantCode:      4,
			wantErr:       "sync: append failed book-adopt: HTTP 401 Unauthorized",
		},
		{
			name:       "追記が 403 で失敗したら残りの Book を書かずに exitcode.Notion の error を返す",
			appendErrs: map[string]error{"page-book-new": notionStatusError(http.StatusForbidden)},
			want: ApplyResult{
				Created: []string{"book-new"},
			},
			wantAppends: map[string][]string{"page-book-new": {"a-1", "a-2", "a-3"}},
			wantCode:    4,
			wantErr:     "sync: append failed book-new: HTTP 403 Forbidden",
		},
		{
			name:       "追記が 404 で失敗したら残りの Book を書かずに exitcode.Notion の error を返す",
			appendErrs: map[string]error{"page-exact": notionStatusError(http.StatusNotFound)},
			want: ApplyResult{
				Created:  []string{"book-new"},
				Adopted:  []string{"book-adopt"},
				Exact:    []string{"book-exact"},
				Appended: 6,
			},
			wantAppends:   map[string][]string{"page-book-new": {"a-1", "a-2", "a-3"}, "page-adopt": {"a-1", "a-2", "a-3"}, "page-exact": {"a-1", "a-2", "a-3"}},
			wantListCalls: []string{"page-adopt", "page-exact"},
			wantCode:      4,
			wantErr:       "sync: append failed book-exact: HTTP 404 Not Found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := &fakeDestination{
				matches: applyMatches, remoteIDs: tt.remoteIDs, listErrs: tt.listErrs,
				appendErrs: tt.appendErrs, appendedBeforeErr: tt.appendedBeforeErr,
			}
			src := &fakeSource{highlights: allThree, errs: tt.srcErrs}
			if tt.noHighlights {
				src.highlights = nil
			}

			got, err := ApplyBooks(context.Background(), applyBooks, src, dst)
			if code := exitcode.Code(err); code != tt.wantCode {
				t.Fatalf("exitcode.Code(err) = %d, want %d (err = %v)", code, tt.wantCode, err)
			}
			if err != nil && err.Error() != tt.wantErr {
				t.Errorf("err = %q, want %q", err.Error(), tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ApplyBooks() = %+v, want %+v", got, tt.want)
			}
			if !reflect.DeepEqual(dst.appends, tt.wantAppends) {
				t.Errorf("appends = %v, want %v", dst.appends, tt.wantAppends)
			}
			if !slices.Equal(dst.listCalls, tt.wantListCalls) {
				t.Errorf("listCalls = %v, want %v", dst.listCalls, tt.wantListCalls)
			}
		})
	}
}

func TestApplyResultPartialFailure(t *testing.T) {
	tests := []struct {
		name     string
		result   ApplyResult
		wantCode int
		wantErr  string
	}{
		{
			name:   "skip も失敗も無ければ nil",
			result: ApplyResult{Created: []string{"book-new"}, Adopted: []string{"book-adopt"}, Exact: []string{"book-exact"}},
		},
		{
			name:     "skip があれば partial failure で Book ID と ambiguous だけを並べる",
			result:   ApplyResult{Exact: []string{"book-exact"}, Skipped: []string{"book-ambiguous"}},
			wantCode: 8,
			wantErr:  "sync: 1 books were not synced: book-ambiguous (ambiguous)",
		},
		{
			name: "失敗があれば partial failure で Book ID と種類だけを並べる",
			result: ApplyResult{Failed: []BookFailure{
				{BookID: "book-new", Kind: BookFailureCreate},
				{BookID: "book-adopt", Kind: BookFailureAdopt},
			}},
			wantCode: 8,
			wantErr:  "sync: 2 books were not synced: book-new (create failed); book-adopt (adopt failed)",
		},
		{
			name: "skip と失敗の両方があれば skip を先に並べる",
			result: ApplyResult{
				Skipped: []string{"book-ambiguous"},
				Failed:  []BookFailure{{BookID: "book-new", Kind: BookFailureCreate}},
			},
			wantCode: 8,
			wantErr:  "sync: 2 books were not synced: book-ambiguous (ambiguous); book-new (create failed)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.result.PartialFailure()
			if code := exitcode.Code(err); code != tt.wantCode {
				t.Fatalf("exitcode.Code(err) = %d, want %d (err = %v)", code, tt.wantCode, err)
			}
			if err != nil && err.Error() != tt.wantErr {
				t.Errorf("err = %q, want %q", err.Error(), tt.wantErr)
			}
		})
	}
}
