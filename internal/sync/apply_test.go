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
	}{
		{
			name:    "none は CreateBook、adoptable は AdoptBook を呼び、exact は何も書かず、ambiguous は skip する",
			books:   applyBooks,
			matches: applyMatches,
			want: ApplyResult{
				Created: []string{"book-new"},
				Adopted: []string{"book-adopt"},
				Exact:   []string{"book-exact"},
				Skipped: []string{"book-ambiguous"},
			},
			wantWrites: []string{"create book-new", "adopt page-adopt book-adopt"},
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
			wantWrites: []string{"create book-new", "adopt page-adopt book-adopt"},
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
			wantWrites: []string{"create book-new", "adopt page-adopt book-adopt"},
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
			wantWrites: []string{"create book-new", "adopt page-adopt book-adopt"},
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

			got, err := ApplyBooks(context.Background(), tt.books, dst)
			if code := exitcode.Code(err); code != tt.wantCode {
				t.Fatalf("exitcode.Code(err) = %d, want %d (err = %v)", code, tt.wantCode, err)
			}
			if err != nil && err.Error() != tt.wantErr {
				t.Errorf("err = %q, want %q", err.Error(), tt.wantErr)
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
