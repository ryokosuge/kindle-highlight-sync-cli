package sync

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/destination"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/notion"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/source"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/state"
)

// applyNow は LastSyncedAt に入る同期の時刻。
var applyNow = time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)

// fakeState は highlightStore のテストダブル。SaveHighlights で渡された行を saves に Book 単位で記録し、以後の ListHighlightIDs に返す。
type fakeState struct {
	rows      map[string]map[string]state.HighlightState // Book ID → Highlight ID → 行
	listErrs  map[string]error                           // Book ID ごとに ListHighlightIDs が返す error
	saveErr   error                                      // SaveHighlights が返す error（行は保存しない）
	listCalls []string
	saves     [][]state.HighlightState
	saveCtxs  []error                    // SaveHighlights が受け取った context の Err()
	books     map[string]state.BookState // Book ID → books の行。MarkBookSynced で上書きする
	marks     []state.BookState          // MarkBookSynced に渡った行の順
	markErr   error                      // MarkBookSynced が返す error（行は記録しない）
}

func (f *fakeState) MarkBookSynced(_ context.Context, b state.BookState) error {
	f.marks = append(f.marks, b)
	if f.markErr != nil {
		return f.markErr
	}
	if f.books == nil {
		f.books = make(map[string]state.BookState)
	}
	f.books[b.KindleBookID] = b
	return nil
}

func (f *fakeState) ListHighlightIDs(_ context.Context, bookID string) (map[string]state.HighlightState, error) {
	f.listCalls = append(f.listCalls, bookID)
	if err, ok := f.listErrs[bookID]; ok {
		return nil, err
	}
	got := make(map[string]state.HighlightState)
	for id, row := range f.rows[bookID] {
		got[id] = row
	}
	return got, nil
}

func (f *fakeState) SaveHighlights(ctx context.Context, states []state.HighlightState) error {
	f.saves = append(f.saves, states)
	f.saveCtxs = append(f.saveCtxs, ctx.Err())
	if f.saveErr != nil {
		return f.saveErr
	}
	if f.rows == nil {
		f.rows = make(map[string]map[string]state.HighlightState)
	}
	for _, st := range states {
		if f.rows[st.KindleBookID] == nil {
			f.rows[st.KindleBookID] = make(map[string]state.HighlightState)
		}
		f.rows[st.KindleBookID][st.KindleHighlightID] = st
	}
	return nil
}

// stateRow は at に同期した、Book の Highlight の State の行。Block ID は fakeDestination が返す "block-<Highlight ID>" の形。
func stateRow(bookID, highlightID, blockID string) state.HighlightState {
	return state.HighlightState{KindleHighlightID: highlightID, KindleBookID: bookID, DestinationHighlightID: blockID, LastSyncedAt: applyNow}
}

// applyBooks は照合の結果が none / adoptable / exact / ambiguous の順に並ぶ 4 冊。タイトルは文言に出ないことを確かめるために入れる。
var applyBooks = []domain.Book{
	{ID: "book-new", ASIN: "ASIN-new", Title: "New Book"},
	{ID: "book-adopt", ASIN: "ASIN-adopt", Title: "Adopt Book"},
	{ID: "book-exact", ASIN: "ASIN-exact", Title: "Exact Book"},
	{ID: "book-ambiguous", ASIN: "ASIN-ambiguous", Title: "Ambiguous Book"},
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
			name:    "ASIN の無い Book は照合も書き込みもせず ASIN が無い失敗に数え、残りの Book は同期する",
			books:   []domain.Book{{ID: "book-no-asin", Title: "No ASIN"}, applyBooks[0]},
			matches: applyMatches,
			want: ApplyResult{
				Created: []string{"book-new"},
				Failed:  []FailedBook{{BookID: "book-no-asin", Kind: FailureNoASIN}},
			},
			wantWrites: []string{"create book-new"},
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
				Failed:  []FailedBook{{BookID: "book-new", Kind: FailureNotionWrite}},
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
				Failed:  []FailedBook{{BookID: "book-adopt", Kind: FailureNotionWrite}},
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
				Failed:  []FailedBook{{BookID: "book-new", Kind: FailureNotionWrite}},
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
			wantErr:    "sync: notion write failed book-new: HTTP 401 Unauthorized",
		},
		{
			name:       "AdoptBook が 403 で失敗したら残りの Book を書かずに exitcode.Notion の error を返す",
			books:      applyBooks,
			matches:    applyMatches,
			writeErrs:  map[string]error{"book-adopt": notionStatusError(http.StatusForbidden)},
			want:       ApplyResult{Created: []string{"book-new"}},
			wantWrites: []string{"create book-new", "adopt page-adopt book-adopt"},
			wantCode:   4,
			wantErr:    "sync: notion write failed book-adopt: HTTP 403 Forbidden",
		},
		{
			name:       "AdoptBook が 404 で失敗したら残りの Book を書かずに exitcode.Notion の error を返す",
			books:      applyBooks,
			matches:    applyMatches,
			writeErrs:  map[string]error{"book-adopt": notionStatusError(http.StatusNotFound)},
			want:       ApplyResult{Created: []string{"book-new"}},
			wantWrites: []string{"create book-new", "adopt page-adopt book-adopt"},
			wantCode:   4,
			wantErr:    "sync: notion write failed book-adopt: HTTP 404 Not Found",
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
				Failed:  []FailedBook{{BookID: "book-exact", Kind: FailureNotionWrite}},
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
				Failed:  []FailedBook{{BookID: "book-exact", Kind: FailureNotionWrite}},
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
				Failed:  []FailedBook{{BookID: "book-exact", Kind: FailureNotionWrite}},
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
			wantErr:    "sync: notion write failed book-exact: HTTP 401 Unauthorized",
		},
		{
			name:       "UpdateBook が 403 で失敗したら残りの Book を書かずに exitcode.Notion の error を返す",
			books:      applyBooks,
			matches:    applyMatches,
			writeErrs:  map[string]error{"book-exact": notionStatusError(http.StatusForbidden)},
			want:       ApplyResult{Created: []string{"book-new"}, Adopted: []string{"book-adopt"}},
			wantWrites: []string{"create book-new", "adopt page-adopt book-adopt", "update page-exact book-exact"},
			wantCode:   4,
			wantErr:    "sync: notion write failed book-exact: HTTP 403 Forbidden",
		},
		{
			name:       "UpdateBook が 404 で失敗したら残りの Book を書かずに exitcode.Notion の error を返す",
			books:      applyBooks,
			matches:    applyMatches,
			writeErrs:  map[string]error{"book-exact": notionStatusError(http.StatusNotFound)},
			want:       ApplyResult{Created: []string{"book-new"}, Adopted: []string{"book-adopt"}},
			wantWrites: []string{"create book-new", "adopt page-adopt book-adopt", "update page-exact book-exact"},
			wantCode:   4,
			wantErr:    "sync: notion write failed book-exact: HTTP 404 Not Found",
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

			got, err := ApplyBooks(context.Background(), tt.books, &fakeSource{}, dst, &fakeState{}, applyNow)
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

var (
	bookFormatError      = exitcode.New(exitcode.SourceFormat, fmt.Errorf("source: highlight row: %w", source.ErrSourceFormat))
	highlightsFrameError = exitcode.New(exitcode.SourceFormat, fmt.Errorf("source: highlights page: %w: %w", source.ErrHighlightsFrame, source.ErrSourceFormat))
	browserError         = exitcode.New(exitcode.Browser, errors.New("browser: page crashed"))
	highlightIDError     = exitcode.New(exitcode.SourceFormat, fmt.Errorf("source: row 1: %w: %w", domain.ErrMissingAnnotationID, source.ErrSourceFormat))
	wrappedDeadlineError = exitcode.New(exitcode.Browser, fmt.Errorf("source: highlights page: %w", context.DeadlineExceeded))
)

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
		writeErrs         map[string]error
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
			name:      "exact は UpdateBook が成功してから追記し、UpdateBook が失敗した exact は取得も追記もしない",
			writeErrs: map[string]error{"book-exact": notionStatusError(http.StatusBadGateway)},
			want: ApplyResult{
				Created:  []string{"book-new"},
				Adopted:  []string{"book-adopt"},
				Skipped:  []string{"book-ambiguous"},
				Failed:   []FailedBook{{BookID: "book-exact", Kind: FailureNotionWrite}},
				Appended: 6,
			},
			wantAppends:   map[string][]string{"page-book-new": {"a-1", "a-2", "a-3"}, "page-adopt": {"a-1", "a-2", "a-3"}},
			wantListCalls: []string{"page-adopt"},
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
				Failed:   []FailedBook{{BookID: "book-adopt", Kind: FailureNotionWrite}},
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
				Failed:   []FailedBook{{BookID: "book-new", Kind: FailureNotionWrite}},
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
				Failed:   []FailedBook{{BookID: "book-adopt", Kind: FailureNotionWrite}},
				Appended: 6,
			},
			wantAppends:   map[string][]string{"page-book-new": {"a-1", "a-2", "a-3"}, "page-exact": {"a-1", "a-2", "a-3"}},
			wantListCalls: []string{"page-adopt", "page-exact"},
		},
		{
			name:    "1 冊だけの構造の異常で Highlight の取得が失敗したら残りの Book を続け、その Book には追記しない",
			srcErrs: map[string]error{"book-exact": bookFormatError},
			want: ApplyResult{
				Created:  []string{"book-new"},
				Adopted:  []string{"book-adopt"},
				Exact:    []string{"book-exact"},
				Skipped:  []string{"book-ambiguous"},
				Failed:   []FailedBook{{BookID: "book-exact", Kind: FailureNotebookStructure}},
				Appended: 6,
			},
			wantAppends:   map[string][]string{"page-book-new": {"a-1", "a-2", "a-3"}, "page-adopt": {"a-1", "a-2", "a-3"}},
			wantListCalls: []string{"page-adopt"},
		},
		{
			name:    "Highlight の取得が Highlight ID を作れず失敗したら残りの Book を続け、その Book には追記しない",
			srcErrs: map[string]error{"book-exact": highlightIDError},
			want: ApplyResult{
				Created:  []string{"book-new"},
				Adopted:  []string{"book-adopt"},
				Exact:    []string{"book-exact"},
				Skipped:  []string{"book-ambiguous"},
				Failed:   []FailedBook{{BookID: "book-exact", Kind: FailureHighlightID}},
				Appended: 6,
			},
			wantAppends:   map[string][]string{"page-book-new": {"a-1", "a-2", "a-3"}, "page-adopt": {"a-1", "a-2", "a-3"}},
			wantListCalls: []string{"page-adopt"},
		},
		{
			name:    "Highlight の取得がタイムアウトしたら種類を保って残りの Book を書かずに止める",
			srcErrs: map[string]error{"book-exact": context.DeadlineExceeded},
			want: ApplyResult{
				Created:  []string{"book-new"},
				Adopted:  []string{"book-adopt"},
				Exact:    []string{"book-exact"},
				Appended: 6,
			},
			wantAppends:   map[string][]string{"page-book-new": {"a-1", "a-2", "a-3"}, "page-adopt": {"a-1", "a-2", "a-3"}},
			wantListCalls: []string{"page-adopt"},
			wantCode:      1,
			wantErr:       context.DeadlineExceeded.Error(),
		},
		{
			name:    "包まれたタイムアウトで Highlight の取得が失敗しても種類を保って残りの Book を書かずに止める",
			srcErrs: map[string]error{"book-adopt": wrappedDeadlineError},
			want: ApplyResult{
				Created:  []string{"book-new"},
				Adopted:  []string{"book-adopt"},
				Appended: 3,
			},
			wantAppends: map[string][]string{"page-book-new": {"a-1", "a-2", "a-3"}},
			wantCode:    5,
			wantErr:     wrappedDeadlineError.Error(),
		},
		{
			name:    "ブラウザの失敗で Highlight が取れなければ種類を保って残りの Book を書かずに止める",
			srcErrs: map[string]error{"book-exact": browserError},
			want: ApplyResult{
				Created:  []string{"book-new"},
				Adopted:  []string{"book-adopt"},
				Exact:    []string{"book-exact"},
				Appended: 6,
			},
			wantAppends:   map[string][]string{"page-book-new": {"a-1", "a-2", "a-3"}, "page-adopt": {"a-1", "a-2", "a-3"}},
			wantListCalls: []string{"page-adopt"},
			wantCode:      5,
			wantErr:       browserError.Error(),
		},
		{
			name:     "Highlight のページの枠の異常は構造の異常でも全 Book に共通なので種類を保って止める",
			srcErrs:  map[string]error{"book-new": highlightsFrameError},
			want:     ApplyResult{Created: []string{"book-new"}},
			wantCode: 7,
			wantErr:  highlightsFrameError.Error(),
		},
		{
			name:     "既知の取得がローカル状態の不整合で失敗したら Book 単位の失敗に数えず、種類を保って止める",
			listErrs: map[string]error{"page-adopt": exitcode.New(exitcode.LocalState, errors.New("sync marker scheme is incompatible"))},
			want: ApplyResult{
				Created:  []string{"book-new"},
				Adopted:  []string{"book-adopt"},
				Appended: 3,
			},
			wantAppends:   map[string][]string{"page-book-new": {"a-1", "a-2", "a-3"}},
			wantListCalls: []string{"page-adopt"},
			wantCode:      6,
			wantErr:       "sync marker scheme is incompatible",
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
			wantErr:       "sync: notion write failed book-adopt: HTTP 401 Unauthorized",
		},
		{
			name:       "追記が 403 で失敗したら残りの Book を書かずに exitcode.Notion の error を返す",
			appendErrs: map[string]error{"page-book-new": notionStatusError(http.StatusForbidden)},
			want: ApplyResult{
				Created: []string{"book-new"},
			},
			wantAppends: map[string][]string{"page-book-new": {"a-1", "a-2", "a-3"}},
			wantCode:    4,
			wantErr:     "sync: notion write failed book-new: HTTP 403 Forbidden",
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
			wantErr:       "sync: notion write failed book-exact: HTTP 404 Not Found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := &fakeDestination{
				matches: applyMatches, writeErrs: tt.writeErrs, remoteIDs: tt.remoteIDs, listErrs: tt.listErrs,
				appendErrs: tt.appendErrs, appendedBeforeErr: tt.appendedBeforeErr,
			}
			src := &fakeSource{highlights: allThree, errs: tt.srcErrs}
			if tt.noHighlights {
				src.highlights = nil
			}

			got, err := ApplyBooks(context.Background(), applyBooks, src, dst, &fakeState{}, applyNow)
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
			if slices.Contains(tt.want.Exact, "book-exact") && !slices.Contains(dst.writes, "update page-exact book-exact") {
				t.Errorf("writes = %v, want UpdateBook for book-exact", dst.writes)
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
			result: ApplyResult{Failed: []FailedBook{
				{BookID: "book-new", Kind: FailureNotionWrite},
				{BookID: "book-adopt", Kind: FailureNotionWrite},
			}},
			wantCode: 8,
			wantErr:  "sync: 2 books were not synced: book-new (notion write failed); book-adopt (notion write failed)",
		},
		{
			name: "skip と失敗の両方があれば skip を先に並べる",
			result: ApplyResult{
				Skipped: []string{"book-ambiguous"},
				Failed:  []FailedBook{{BookID: "book-new", Kind: FailureNotionWrite}},
			},
			wantCode: 8,
			wantErr:  "sync: 2 books were not synced: book-ambiguous (ambiguous); book-new (notion write failed)",
		},
		{
			name: "Book 単位の取得の失敗と ASIN が無い Book も種類の名前で並ぶ",
			result: ApplyResult{
				Failed: []FailedBook{{BookID: "book-a", Kind: FailureNoASIN}, {BookID: "book-b", Kind: FailureNotebookStructure}, {BookID: "book-c", Kind: FailureHighlightID}},
			},
			wantCode: 8,
			wantErr:  "sync: 3 books were not synced: book-a (book has no ASIN); book-b (unexpected notebook structure); book-c (highlight ID cannot be made)",
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

func TestApplyBooksState(t *testing.T) {
	exactBook := []domain.Book{applyBooks[2]}
	tests := []struct {
		name          string
		books         []domain.Book
		rows          map[string]map[string]state.HighlightState
		remoteIDs     map[string][]string
		appendErrs    map[string]error
		appendedFirst map[string]int
		listErrs      map[string]error
		saveErr       error
		canceled      bool // ApplyBooks にキャンセル済みの context を渡す
		wantCanceled  bool // 返る error が context.Canceled であること
		want          ApplyResult
		wantAppends   map[string][]string
		wantDstList   []string
		wantSaves     [][]state.HighlightState
		wantCode      int
	}{
		{
			name:  "Kindle の Highlight がすべて State にある Book は Notion の一覧も追記も呼ばず、保存もしない",
			books: exactBook,
			rows: map[string]map[string]state.HighlightState{"book-exact": {
				"a-1": stateRow("book-exact", "a-1", "b-1"), "a-2": stateRow("book-exact", "a-2", "b-2"), "a-3": stateRow("book-exact", "a-3", "b-3"),
			}},
			want: ApplyResult{Exact: []string{"book-exact"}},
		},
		{
			name:        "State に無い Highlight がある Book は Notion の一覧を取り、State にも Notion にも無い分だけを追記して、Notion にあった分と追記した分を 1 回で保存する",
			books:       exactBook,
			rows:        map[string]map[string]state.HighlightState{"book-exact": {"a-1": stateRow("book-exact", "a-1", "b-1")}},
			remoteIDs:   map[string][]string{"page-exact": {"a-1", "a-2"}},
			want:        ApplyResult{Exact: []string{"book-exact"}, Appended: 1},
			wantAppends: map[string][]string{"page-exact": {"a-3"}},
			wantDstList: []string{"page-exact"},
			wantSaves: [][]state.HighlightState{{
				stateRow("book-exact", "a-2", "block-1"), stateRow("book-exact", "a-3", "block-a-3"),
			}},
		},
		{
			name:        "作成した Book は State も Notion の一覧も見ず、追記した全部を保存する",
			books:       applyBooks[:1],
			want:        ApplyResult{Created: []string{"book-new"}, Appended: 3},
			wantAppends: map[string][]string{"page-book-new": {"a-1", "a-2", "a-3"}},
			wantSaves: [][]state.HighlightState{{
				stateRow("book-new", "a-1", "block-a-1"), stateRow("book-new", "a-2", "block-a-2"), stateRow("book-new", "a-3", "block-a-3"),
			}},
		},
		{
			name:          "追記が一部だけ成功した Book は成功した分だけを保存してから、失敗を Failed に残す",
			books:         exactBook,
			appendErrs:    map[string]error{"page-exact": notionStatusError(http.StatusBadGateway)},
			appendedFirst: map[string]int{"page-exact": 2},
			want:          ApplyResult{Exact: []string{"book-exact"}, Failed: []FailedBook{{BookID: "book-exact", Kind: FailureNotionWrite}}, Appended: 2},
			wantAppends:   map[string][]string{"page-exact": {"a-1", "a-2", "a-3"}},
			wantDstList:   []string{"page-exact"},
			wantSaves:     [][]state.HighlightState{{stateRow("book-exact", "a-1", "block-a-1"), stateRow("book-exact", "a-2", "block-a-2")}},
		},
		{
			name:          "追記が 401 で失敗しても成功した分を保存してから exitcode.Notion の error を返す",
			books:         exactBook,
			appendErrs:    map[string]error{"page-exact": notionStatusError(http.StatusUnauthorized)},
			appendedFirst: map[string]int{"page-exact": 1},
			want:          ApplyResult{Exact: []string{"book-exact"}, Appended: 1},
			wantAppends:   map[string][]string{"page-exact": {"a-1", "a-2", "a-3"}},
			wantDstList:   []string{"page-exact"},
			wantSaves:     [][]state.HighlightState{{stateRow("book-exact", "a-1", "block-a-1")}},
			wantCode:      4,
		},
		{
			name:          "追記が部分成功してキャンセルされても、キャンセルされていない context で成功した分を保存し、元のキャンセルの error を返す",
			books:         exactBook,
			canceled:      true,
			appendErrs:    map[string]error{"page-exact": notionCanceledError},
			appendedFirst: map[string]int{"page-exact": 2},
			want:          ApplyResult{Exact: []string{"book-exact"}, Appended: 2},
			wantAppends:   map[string][]string{"page-exact": {"a-1", "a-2", "a-3"}},
			wantDstList:   []string{"page-exact"},
			wantSaves:     [][]state.HighlightState{{stateRow("book-exact", "a-1", "block-a-1"), stateRow("book-exact", "a-2", "block-a-2")}},
			wantCanceled:  true,
			wantCode:      4, // fail は Notion client が返した元の error をそのまま返す（LocalState の 6 ではない）
		},
		{
			name:     "State の読み取りが失敗したら、残りの Book を書かずに exitcode.LocalState の error を返す",
			books:    applyBooks[1:3],
			listErrs: map[string]error{"book-adopt": errors.New("state: open state.db")},
			want:     ApplyResult{Adopted: []string{"book-adopt"}},
			wantCode: 6,
		},
		{
			name:         "State の読み取りがキャンセルで失敗したら exitcode.LocalState に変えず、残りの Book を書かずに中断の error を返す",
			books:        applyBooks[1:3],
			canceled:     true,
			listErrs:     map[string]error{"book-adopt": exitcode.New(exitcode.LocalState, fmt.Errorf("state: begin: %w", context.Canceled))},
			want:         ApplyResult{Adopted: []string{"book-adopt"}},
			wantCanceled: true,
			wantCode:     1,
		},
		{
			name:     "State の読み取りが DeadlineExceeded で失敗したら、残りの Book を書かずに exitcode.LocalState の error を返す",
			books:    applyBooks[1:3],
			listErrs: map[string]error{"book-adopt": exitcode.New(exitcode.LocalState, fmt.Errorf("state: begin: %w", context.DeadlineExceeded))},
			want:     ApplyResult{Adopted: []string{"book-adopt"}},
			wantCode: 6,
		},
		{
			name:        "State の保存が失敗したら、残りの Book を書かずに exitcode.LocalState の error を返す",
			books:       applyBooks[:3],
			saveErr:     errors.New("state: commit"),
			want:        ApplyResult{Created: []string{"book-new"}, Appended: 3},
			wantAppends: map[string][]string{"page-book-new": {"a-1", "a-2", "a-3"}},
			wantSaves: [][]state.HighlightState{{
				stateRow("book-new", "a-1", "block-a-1"), stateRow("book-new", "a-2", "block-a-2"), stateRow("book-new", "a-3", "block-a-3"),
			}},
			wantCode: 6,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := &fakeDestination{
				matches: applyMatches, remoteIDs: tt.remoteIDs,
				appendErrs: tt.appendErrs, appendedBeforeErr: tt.appendedFirst,
			}
			src := &fakeSource{highlights: map[string][]domain.Highlight{}}
			for _, b := range applyBooks {
				src.highlights[b.ID] = applyHighlights("a-1", "a-2", "a-3")
			}
			st := &fakeState{rows: tt.rows, listErrs: tt.listErrs, saveErr: tt.saveErr}

			ctx := context.Background()
			if tt.canceled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			got, err := ApplyBooks(ctx, tt.books, src, dst, st, applyNow)
			if code := exitcode.Code(err); code != tt.wantCode {
				t.Fatalf("exitcode.Code(err) = %d, want %d (err = %v)", code, tt.wantCode, err)
			}
			if gotCanceled := errors.Is(err, context.Canceled); gotCanceled != tt.wantCanceled {
				t.Errorf("errors.Is(err, context.Canceled) = %v, want %v (err = %v)", gotCanceled, tt.wantCanceled, err)
			}
			for i, ctxErr := range st.saveCtxs {
				if ctxErr != nil {
					t.Errorf("SaveHighlights の context.Err() [%d] = %v, want %v", i, ctxErr, nil)
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ApplyBooks() = %+v, want %+v", got, tt.want)
			}
			if !reflect.DeepEqual(dst.appends, tt.wantAppends) {
				t.Errorf("appends = %v, want %v", dst.appends, tt.wantAppends)
			}
			if !slices.Equal(dst.listCalls, tt.wantDstList) {
				t.Errorf("listCalls = %v, want %v", dst.listCalls, tt.wantDstList)
			}
			if !reflect.DeepEqual(st.saves, tt.wantSaves) {
				t.Errorf("saves = %v, want %v", st.saves, tt.wantSaves)
			}
		})
	}
}

func TestApplyBooksStateTwice(t *testing.T) {
	books := []domain.Book{applyBooks[2]}
	dst := &fakeDestination{matches: applyMatches}
	src := &fakeSource{highlights: map[string][]domain.Highlight{"book-exact": applyHighlights("a-1", "a-2")}}
	st := &fakeState{}

	first, err := ApplyBooks(context.Background(), books, src, dst, st, applyNow)
	if err != nil {
		t.Fatalf("ApplyBooks() 1 回目 error = %v", err)
	}
	if first.Appended != 2 {
		t.Errorf("1 回目の Appended = %d, want %d", first.Appended, 2)
	}
	appendsAfterFirst := len(dst.appends["page-exact"])

	second, err := ApplyBooks(context.Background(), books, src, dst, st, applyNow)
	if err != nil {
		t.Fatalf("ApplyBooks() 2 回目 error = %v", err)
	}
	if second.Appended != 0 {
		t.Errorf("2 回目の Appended = %d, want %d", second.Appended, 0)
	}
	if got := len(dst.appends["page-exact"]); got != appendsAfterFirst {
		t.Errorf("2 回目の追記した Highlight の数 = %d, want %d", got-appendsAfterFirst, 0)
	}
	if want := []string{"page-exact"}; !slices.Equal(dst.listCalls, want) {
		t.Errorf("listCalls = %v, want %v", dst.listCalls, want)
	}
}

func TestApplyBooksMarkSynced(t *testing.T) {
	annotated := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	earlier := applyNow.Add(-24 * time.Hour)
	// applyBooks と同じ照合の 4 冊に、記録に入る ASIN と Last Annotated Date を足したもの
	books := make([]domain.Book, len(applyBooks))
	for i, b := range applyBooks {
		b.ASIN = "asin-" + b.ID
		b.LastAnnotatedAt = &annotated
		books[i] = b
	}
	mark := func(id, page string) state.BookState {
		return state.BookState{KindleBookID: id, DestinationBookID: page, ASIN: "asin-" + id, Title: map[string]string{"book-new": "New Book", "book-adopt": "Adopt Book", "book-exact": "Exact Book"}[id], LastAnnotatedAt: &annotated, LastSyncedAt: applyNow}
	}
	allThree := map[string][]domain.Highlight{}
	for _, b := range books {
		allThree[b.ID] = applyHighlights("a-1", "a-2", "a-3")
	}
	prior := state.BookState{KindleBookID: "book-exact", DestinationBookID: "page-exact", LastSyncedAt: earlier}
	tests := []struct {
		name         string
		noHighlights bool
		writeErrs    map[string]error
		srcErrs      map[string]error
		appendErrs   map[string]error
		appended     map[string]int
		markErr      error
		wantMarks    []state.BookState
		wantBooks    map[string]state.BookState
		wantCode     int
	}{
		{
			name:      "追記がすべて成功した Book は記録され、ambiguous は記録されない",
			wantMarks: []state.BookState{mark("book-new", "page-book-new"), mark("book-adopt", "page-adopt"), mark("book-exact", "page-exact")},
		},
		{
			name:         "追記するものが無かった Book も記録される",
			noHighlights: true,
			wantMarks:    []state.BookState{mark("book-new", "page-book-new"), mark("book-adopt", "page-adopt"), mark("book-exact", "page-exact")},
		},
		{
			name:       "追記が一部だけ成功した Book は記録されず、前回の記録も変わらない",
			appendErrs: map[string]error{"page-exact": notionTimeoutError},
			appended:   map[string]int{"page-exact": 1},
			wantMarks:  []state.BookState{mark("book-new", "page-book-new"), mark("book-adopt", "page-adopt")},
			wantBooks:  map[string]state.BookState{"book-exact": prior},
		},
		{
			name:      "Highlight の取得が失敗した Book は記録されず、前回の記録も変わらない",
			srcErrs:   map[string]error{"book-exact": bookFormatError},
			wantMarks: []state.BookState{mark("book-new", "page-book-new"), mark("book-adopt", "page-adopt")},
			wantBooks: map[string]state.BookState{"book-exact": prior},
		},
		{
			name:      "作成と Adopt に失敗した Book は記録されない",
			writeErrs: map[string]error{"book-new": notionStatusError(http.StatusInternalServerError), "book-adopt": notionStatusError(http.StatusBadRequest)},
			wantMarks: []state.BookState{mark("book-exact", "page-exact")},
		},
		{
			name:      "記録が失敗したら残りの Book を書かずに LocalState で返す",
			markErr:   exitcode.New(exitcode.LocalState, errors.New("disk full")),
			wantMarks: []state.BookState{mark("book-new", "page-book-new")},
			wantCode:  int(exitcode.LocalState),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &fakeSource{highlights: allThree, errs: tt.srcErrs}
			if tt.noHighlights {
				src.highlights = nil
			}
			dst := &fakeDestination{matches: applyMatches, writeErrs: tt.writeErrs, appendErrs: tt.appendErrs, appendedBeforeErr: tt.appended}
			st := &fakeState{markErr: tt.markErr, books: map[string]state.BookState{"book-exact": prior}}
			_, err := ApplyBooks(context.Background(), books, src, dst, st, applyNow)
			if got := exitcode.Code(err); got != tt.wantCode {
				t.Errorf("exitcode.Code(err) = %d, want %d (err = %v)", got, tt.wantCode, err)
			}
			if !reflect.DeepEqual(st.marks, tt.wantMarks) {
				t.Errorf("marks = %v, want %v", st.marks, tt.wantMarks)
			}
			for id, want := range tt.wantBooks {
				if got := st.books[id]; !reflect.DeepEqual(got, want) {
					t.Errorf("books[%q] = %v, want %v", id, got, want)
				}
			}
		})
	}

	t.Run("1 回目に追記が一部失敗した Book は 2 回目に残りが追記されると記録される", func(t *testing.T) {
		exact := []domain.Book{books[2]}
		src := &fakeSource{highlights: allThree}
		dst := &fakeDestination{matches: applyMatches, appendErrs: map[string]error{"page-exact": notionTimeoutError}, appendedBeforeErr: map[string]int{"page-exact": 1}}
		st := &fakeState{}
		if _, err := ApplyBooks(context.Background(), exact, src, dst, st, applyNow); err != nil {
			t.Fatalf("ApplyBooks() 1 回目 error = %v", err)
		}
		if len(st.marks) != 0 {
			t.Fatalf("1 回目の marks = %v, want 空", st.marks)
		}
		dst.appendErrs = nil
		if _, err := ApplyBooks(context.Background(), exact, src, dst, st, applyNow); err != nil {
			t.Fatalf("ApplyBooks() 2 回目 error = %v", err)
		}
		if want := []state.BookState{mark("book-exact", "page-exact")}; !reflect.DeepEqual(st.marks, want) {
			t.Errorf("2 回目の marks = %v, want %v", st.marks, want)
		}
	})
}

// unknownOutcomeError は Notion client が実際に返す、結果不明の失敗の error（httptest の応答から作る）。retryAfter が空でなければ Retry-After に入れる。
func unknownOutcomeError(t *testing.T, status int, retryAfter string) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	_, err := notion.New("test-token", notion.WithBaseURL(srv.URL)).AppendBlockChildren(context.Background(), "page", nil)
	if err == nil {
		t.Fatalf("status %d: err = nil", status)
	}
	return err
}

// appendStep は recheckDestination の AppendHighlights の 1 回分の応答。渡された先頭から reflect 件だけ反映し、err があれば返す。
type appendStep struct {
	reflect int
	err     error
}

// recheckDestination は追記で反映した分を ListHighlightIDs が返す fakeDestination。AppendHighlights は steps の順に応答し、尽きたら全部反映して成功する。
// 反映したがその分を返さない（結果不明）ことを unreturned で表す。
type recheckDestination struct {
	*fakeDestination
	pages      map[string][]string // ページ ID → Notion に在る Highlight ID の順
	steps      map[string][]appendStep
	unreturned bool // true のとき、反映した分を AppendHighlights の戻り値に含めない
	listErrs   []error
	sent       map[string][][]string // ページ ID → AppendHighlights に渡った Highlight ID の呼び出しごとの順
}

func (f *recheckDestination) AppendHighlights(_ context.Context, remote destination.RemoteBook, hs []domain.Highlight) ([]destination.RemoteHighlight, error) {
	if f.sent == nil {
		f.sent = make(map[string][][]string)
	}
	ids := make([]string, 0, len(hs))
	for _, h := range hs {
		ids = append(ids, h.ID)
	}
	f.sent[remote.ID] = append(f.sent[remote.ID], ids)
	step := appendStep{reflect: len(hs)}
	if q := f.steps[remote.ID]; len(q) > 0 {
		step, f.steps[remote.ID] = q[0], q[1:]
	}
	var returned []destination.RemoteHighlight
	for _, h := range hs[:step.reflect] {
		f.pages[remote.ID] = append(f.pages[remote.ID], h.ID)
		if !f.unreturned || step.err == nil {
			returned = append(returned, destination.RemoteHighlight{ID: "block-" + h.ID, HighlightID: h.ID})
		}
	}
	return returned, step.err
}

func (f *recheckDestination) ListHighlightIDs(_ context.Context, remote destination.RemoteBook) (map[string]destination.RemoteHighlight, error) {
	f.listCalls = append(f.listCalls, remote.ID)
	if len(f.listErrs) > 0 && len(f.listCalls) > 1 { // 1 回目は追記の前の既知の取得。error は確かめ直しの取得に使う
		err := f.listErrs[0]
		f.listErrs = f.listErrs[1:]
		if err != nil {
			return nil, err
		}
	}
	known := make(map[string]destination.RemoteHighlight)
	for _, id := range f.pages[remote.ID] {
		known[id] = destination.RemoteHighlight{ID: "block-" + id, HighlightID: id}
	}
	return known, nil
}

func TestApplyBooksRecheck(t *testing.T) {
	books := []domain.Book{{ID: "book-a", ASIN: "ASIN-a"}, {ID: "book-b", ASIN: "ASIN-b"}}
	matches := map[string]destination.BookMatch{
		"book-a": {Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "page-a"}},
		"book-b": {Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "page-b"}},
	}
	highlights := map[string][]domain.Highlight{
		"book-a": applyHighlights("a-1", "a-2", "a-3"),
		"book-b": applyHighlights("b-1"),
	}
	err503 := unknownOutcomeError(t, http.StatusServiceUnavailable, "")
	err429 := unknownOutcomeError(t, http.StatusTooManyRequests, "")
	err429After := unknownOutcomeError(t, http.StatusTooManyRequests, "7")
	err429Long := unknownOutcomeError(t, http.StatusTooManyRequests, "61")

	tests := []struct {
		name         string
		steps        map[string][]appendStep
		unreturned   bool
		listErrs     []error
		want         ApplyResult
		wantSent     map[string][][]string
		wantPages    map[string][]string
		wantSaved    []string // State に保存された book-a の Highlight ID（保存順）
		wantSleeps   []time.Duration
		wantCode     int
		wantListCall int
	}{
		{
			name:       "反映したのに結果不明で返ったときは、確かめ直しで在ると分かった分を送らず成功に数える",
			steps:      map[string][]appendStep{"page-a": {{reflect: 3, err: err503}}},
			unreturned: true,
			want:       ApplyResult{Exact: []string{"book-a", "book-b"}, Appended: 3 + 1, Rechecked: 1},
			wantSent:   map[string][][]string{"page-a": {{"a-1", "a-2", "a-3"}}, "page-b": {{"b-1"}}},
			wantPages:  map[string][]string{"page-a": {"a-1", "a-2", "a-3"}, "page-b": {"b-1"}},
			wantSaved:  []string{"a-1", "a-2", "a-3"},
			wantSleeps: []time.Duration{time.Second},
		},
		{
			name:       "一部を反映して結果不明になったら、未反映の分だけを Kindle の順のまま送り直し、同じ Highlight は 1 つだけになる",
			steps:      map[string][]appendStep{"page-a": {{reflect: 2, err: err503}}},
			unreturned: true,
			want:       ApplyResult{Exact: []string{"book-a", "book-b"}, Appended: 3 + 1, Rechecked: 1, Resent: 1},
			wantSent:   map[string][][]string{"page-a": {{"a-1", "a-2", "a-3"}, {"a-3"}}, "page-b": {{"b-1"}}},
			wantPages:  map[string][]string{"page-a": {"a-1", "a-2", "a-3"}, "page-b": {"b-1"}},
			wantSaved:  []string{"a-1", "a-2", "a-3"},
			wantSleeps: []time.Duration{time.Second},
		},
		{
			name:       "何も反映していなければ全部を送り直す",
			steps:      map[string][]appendStep{"page-a": {{reflect: 0, err: err503}}},
			want:       ApplyResult{Exact: []string{"book-a", "book-b"}, Appended: 3 + 1, Rechecked: 1, Resent: 3},
			wantSent:   map[string][][]string{"page-a": {{"a-1", "a-2", "a-3"}, {"a-1", "a-2", "a-3"}}, "page-b": {{"b-1"}}},
			wantPages:  map[string][]string{"page-a": {"a-1", "a-2", "a-3"}, "page-b": {"b-1"}},
			wantSaved:  []string{"a-1", "a-2", "a-3"},
			wantSleeps: []time.Duration{time.Second},
		},
		{
			name:       "確かめ直しと再送は 2 回までで、尽きても未反映が残る Book は失敗に残り、他の Book は続く。保存されるのは確かな分だけ",
			steps:      map[string][]appendStep{"page-a": {{reflect: 1, err: err503}, {reflect: 0, err: err503}, {reflect: 0, err: err503}}},
			unreturned: true,
			want: ApplyResult{
				Exact: []string{"book-a", "book-b"}, Failed: []FailedBook{{BookID: "book-a", Kind: FailureNotionWrite}},
				Appended: 1 + 1, Rechecked: 1, Resent: 2 + 2,
			},
			wantSent:   map[string][][]string{"page-a": {{"a-1", "a-2", "a-3"}, {"a-2", "a-3"}, {"a-2", "a-3"}}, "page-b": {{"b-1"}}},
			wantPages:  map[string][]string{"page-a": {"a-1"}, "page-b": {"b-1"}},
			wantSaved:  []string{"a-1"},
			wantSleeps: []time.Duration{time.Second, 2 * time.Second},
		},
		{
			name:       "429 で Retry-After があればその長さを待つ",
			steps:      map[string][]appendStep{"page-a": {{reflect: 0, err: err429After}}},
			want:       ApplyResult{Exact: []string{"book-a", "book-b"}, Appended: 3 + 1, Rechecked: 1, Resent: 3},
			wantSent:   map[string][][]string{"page-a": {{"a-1", "a-2", "a-3"}, {"a-1", "a-2", "a-3"}}, "page-b": {{"b-1"}}},
			wantPages:  map[string][]string{"page-a": {"a-1", "a-2", "a-3"}, "page-b": {"b-1"}},
			wantSaved:  []string{"a-1", "a-2", "a-3"},
			wantSleeps: []time.Duration{7 * time.Second},
		},
		{
			name:       "429 で Retry-After が 60 秒を超えるなら待たず確かめ直しもせず、その Book を失敗にする",
			steps:      map[string][]appendStep{"page-a": {{reflect: 1, err: err429Long}}},
			unreturned: true,
			want: ApplyResult{
				Exact: []string{"book-a", "book-b"}, Failed: []FailedBook{{BookID: "book-a", Kind: FailureNotionWrite}},
				Appended: 0 + 1,
			},
			wantSent:  map[string][][]string{"page-a": {{"a-1", "a-2", "a-3"}}, "page-b": {{"b-1"}}},
			wantPages: map[string][]string{"page-a": {"a-1"}, "page-b": {"b-1"}},
		},
		{
			name:       "429 で Retry-After が無いときは 1 秒・2 秒を待つ",
			steps:      map[string][]appendStep{"page-a": {{reflect: 0, err: err429}, {reflect: 0, err: err429}}},
			want:       ApplyResult{Exact: []string{"book-a", "book-b"}, Appended: 3 + 1, Rechecked: 1, Resent: 6},
			wantSent:   map[string][][]string{"page-a": {{"a-1", "a-2", "a-3"}, {"a-1", "a-2", "a-3"}, {"a-1", "a-2", "a-3"}}, "page-b": {{"b-1"}}},
			wantPages:  map[string][]string{"page-a": {"a-1", "a-2", "a-3"}, "page-b": {"b-1"}},
			wantSaved:  []string{"a-1", "a-2", "a-3"},
			wantSleeps: []time.Duration{time.Second, 2 * time.Second},
		},
		{
			name:      "400 は確かめ直さず Book の失敗にする",
			steps:     map[string][]appendStep{"page-a": {{reflect: 1, err: notionStatusError(http.StatusBadRequest)}}},
			want:      ApplyResult{Exact: []string{"book-a", "book-b"}, Failed: []FailedBook{{BookID: "book-a", Kind: FailureNotionWrite}}, Appended: 1 + 1},
			wantSent:  map[string][][]string{"page-a": {{"a-1", "a-2", "a-3"}}, "page-b": {{"b-1"}}},
			wantPages: map[string][]string{"page-a": {"a-1"}, "page-b": {"b-1"}},
			wantSaved: []string{"a-1"},
		},
		{
			name:      "401 は確かめ直さず止まる",
			steps:     map[string][]appendStep{"page-a": {{reflect: 0, err: notionStatusError(http.StatusUnauthorized)}}},
			want:      ApplyResult{Exact: []string{"book-a"}},
			wantSent:  map[string][][]string{"page-a": {{"a-1", "a-2", "a-3"}}},
			wantPages: map[string][]string{"page-a": nil},
			wantCode:  4,
		},
		{
			name:       "確かめ直しの一覧が 500 で失敗した Book は失敗に残り、他の Book は続く",
			steps:      map[string][]appendStep{"page-a": {{reflect: 1, err: err503}}},
			listErrs:   []error{notionStatusError(http.StatusInternalServerError)},
			want:       ApplyResult{Exact: []string{"book-a", "book-b"}, Failed: []FailedBook{{BookID: "book-a", Kind: FailureNotionWrite}}, Appended: 1 + 1, Rechecked: 1},
			wantSent:   map[string][][]string{"page-a": {{"a-1", "a-2", "a-3"}}, "page-b": {{"b-1"}}},
			wantPages:  map[string][]string{"page-a": {"a-1"}, "page-b": {"b-1"}},
			wantSaved:  []string{"a-1"},
			wantSleeps: []time.Duration{time.Second},
		},
		{
			name:       "確かめ直しの一覧が 401 で失敗したら止まる",
			steps:      map[string][]appendStep{"page-a": {{reflect: 1, err: err503}}},
			listErrs:   []error{notionStatusError(http.StatusUnauthorized)},
			want:       ApplyResult{Exact: []string{"book-a"}, Appended: 1, Rechecked: 1},
			wantSent:   map[string][][]string{"page-a": {{"a-1", "a-2", "a-3"}}},
			wantPages:  map[string][]string{"page-a": {"a-1"}},
			wantSaved:  []string{"a-1"},
			wantSleeps: []time.Duration{time.Second},
			wantCode:   4,
		},
		{
			name:       "確かめ直しの一覧が Sync Marker の prefix 違い（exit 6）で失敗したら止まる",
			steps:      map[string][]appendStep{"page-a": {{reflect: 1, err: err503}}},
			listErrs:   []error{exitcode.New(exitcode.LocalState, errors.New("sync marker scheme is incompatible"))},
			want:       ApplyResult{Exact: []string{"book-a"}, Appended: 1, Rechecked: 1},
			wantSent:   map[string][][]string{"page-a": {{"a-1", "a-2", "a-3"}}},
			wantPages:  map[string][]string{"page-a": {"a-1"}},
			wantSaved:  []string{"a-1"},
			wantSleeps: []time.Duration{time.Second},
			wantCode:   6,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sleeps []time.Duration
			oldSleep, oldJitter := recheckSleep, recheckJitter
			recheckSleep = func(_ context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil }
			recheckJitter = func() time.Duration { return 0 }
			t.Cleanup(func() { recheckSleep, recheckJitter = oldSleep, oldJitter })

			dst := &recheckDestination{
				fakeDestination: &fakeDestination{matches: matches},
				pages:           map[string][]string{}, steps: tt.steps, unreturned: tt.unreturned, listErrs: tt.listErrs,
			}
			st := &fakeState{}
			got, err := ApplyBooks(context.Background(), books, &fakeSource{highlights: highlights}, dst, st, applyNow)
			if code := exitcode.Code(err); code != tt.wantCode {
				t.Fatalf("exitcode.Code(err) = %d, want %d (err = %v)", code, tt.wantCode, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ApplyBooks() = %+v, want %+v", got, tt.want)
			}
			if !reflect.DeepEqual(dst.sent, tt.wantSent) {
				t.Errorf("sent = %v, want %v", dst.sent, tt.wantSent)
			}
			for page, ids := range tt.wantPages {
				if !slices.Equal(dst.pages[page], ids) {
					t.Errorf("pages[%s] = %v, want %v", page, dst.pages[page], ids)
				}
			}
			if !slices.Equal(sleeps, tt.wantSleeps) {
				t.Errorf("sleeps = %v, want %v", sleeps, tt.wantSleeps)
			}
			var saved []string
			for _, rows := range st.saves {
				for _, row := range rows {
					if row.KindleBookID == "book-a" {
						saved = append(saved, row.KindleHighlightID)
						if want := "block-" + row.KindleHighlightID; row.DestinationHighlightID != want {
							t.Errorf("Block ID of %s = %q, want %q", row.KindleHighlightID, row.DestinationHighlightID, want)
						}
					}
				}
			}
			if !slices.Equal(saved, tt.wantSaved) {
				t.Errorf("saved = %v, want %v", saved, tt.wantSaved)
			}
		})
	}
}

func TestApplyBooksRecheckCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	oldSleep := recheckSleep
	recheckSleep = sleepContext
	t.Cleanup(func() { recheckSleep = oldSleep })

	books := []domain.Book{{ID: "book-a", ASIN: "ASIN-a"}}
	dst := &recheckDestination{
		fakeDestination: &fakeDestination{matches: map[string]destination.BookMatch{"book-a": {Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "page-a"}}}},
		pages:           map[string][]string{},
		steps:           map[string][]appendStep{"page-a": {{reflect: 0, err: unknownOutcomeError(t, http.StatusServiceUnavailable, "")}}},
	}
	cancel()
	_, err := ApplyBooks(ctx, books, &fakeSource{highlights: map[string][]domain.Highlight{"book-a": applyHighlights("a-1")}}, dst, &fakeState{}, applyNow)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(dst.listCalls) != 1 {
		t.Errorf("listCalls = %v, want 追記の前の 1 回だけ（待たずに抜ける）", dst.listCalls)
	}
}

func TestApplyBooksRefindCreate(t *testing.T) {
	unknown := unknownOutcomeError(t, http.StatusServiceUnavailable, "")
	badRequest := unknownOutcomeError(t, http.StatusBadRequest, "")
	created := destination.BookMatch{Status: destination.BookMatchExact, Book: &destination.RemoteBook{ID: "page-created"}}
	none := destination.BookMatch{Status: destination.BookMatchNone}
	one := []domain.Book{{ID: "book-new", ASIN: "ASIN-new", Title: "New Book"}}
	failedCreate := []FailedBook{{BookID: "book-new", Kind: FailureNotionWrite}}
	tests := []struct {
		name        string
		books       []domain.Book
		createErrs  []error
		refinds     []destination.BookMatch
		refindErr   error
		want        ApplyResult
		wantWrites  []string
		wantRefinds []string
		wantAppends map[string][]string
		wantWaits   int
		wantCode    int
		wantErr     string
	}{
		{
			name:        "結果が分からない失敗のあと exact なら作成済みのページに追記し、CreateBook は呼び直さない",
			books:       one,
			createErrs:  []error{unknown},
			refinds:     []destination.BookMatch{created},
			want:        ApplyResult{Refound: []string{"book-new"}, Recovered: []string{"book-new"}, Appended: 3},
			wantWrites:  []string{"create book-new"},
			wantRefinds: []string{"book-new"},
			wantAppends: map[string][]string{"page-created": {"a-1", "a-2", "a-3"}},
		},
		{
			name:        "1 回目が none でも待ってからの 2 回目が exact なら作成済みのページを使う",
			books:       one,
			createErrs:  []error{unknown},
			refinds:     []destination.BookMatch{none, created},
			want:        ApplyResult{Refound: []string{"book-new"}, Recovered: []string{"book-new"}, Appended: 3},
			wantWrites:  []string{"create book-new"},
			wantRefinds: []string{"book-new", "book-new"},
			wantAppends: map[string][]string{"page-created": {"a-1", "a-2", "a-3"}},
			wantWaits:   1,
		},
		{
			name:        "2 回続けて none なら CreateBook をもう 1 回だけ呼ぶ",
			books:       one,
			createErrs:  []error{unknown},
			refinds:     []destination.BookMatch{none},
			want:        ApplyResult{Created: []string{"book-new"}, Refound: []string{"book-new"}, Appended: 3},
			wantWrites:  []string{"create book-new", "create book-new"},
			wantRefinds: []string{"book-new", "book-new"},
			wantAppends: map[string][]string{"page-book-new": {"a-1", "a-2", "a-3"}},
			wantWaits:   1,
		},
		{
			name:        "作り直しの CreateBook が失敗したら照合し直さずその Book が失敗として残る",
			books:       one,
			createErrs:  []error{unknown, unknown},
			refinds:     []destination.BookMatch{none},
			want:        ApplyResult{Refound: []string{"book-new"}, Failed: failedCreate},
			wantWrites:  []string{"create book-new", "create book-new"},
			wantRefinds: []string{"book-new", "book-new"},
			wantWaits:   1,
		},
		{
			name:        "照合し直しが ambiguous なら CreateBook も AdoptBook も呼ばず失敗として残り、残りの Book は続く",
			books:       []domain.Book{{ID: "book-new", ASIN: "ASIN-new"}, {ID: "book-next", ASIN: "ASIN-next"}},
			createErrs:  []error{unknown},
			refinds:     []destination.BookMatch{{Status: destination.BookMatchAmbiguous}},
			want:        ApplyResult{Created: []string{"book-next"}, Refound: []string{"book-new"}, Failed: failedCreate, Appended: 3},
			wantWrites:  []string{"create book-new", "create book-next"},
			wantRefinds: []string{"book-new"},
			wantAppends: map[string][]string{"page-book-next": {"a-1", "a-2", "a-3"}},
		},
		{
			name:        "照合し直しが adoptable なら CreateBook も AdoptBook も呼ばず失敗として残る",
			books:       one,
			createErrs:  []error{unknown},
			refinds:     []destination.BookMatch{{Status: destination.BookMatchAdoptable, Book: &destination.RemoteBook{ID: "page-adopt"}}},
			want:        ApplyResult{Refound: []string{"book-new"}, Failed: failedCreate},
			wantWrites:  []string{"create book-new"},
			wantRefinds: []string{"book-new"},
		},
		{
			name:        "照合し直しの読み取りが 500 で失敗したら CreateBook も AdoptBook も呼ばず失敗として残る",
			books:       one,
			createErrs:  []error{unknown},
			refindErr:   notionStatusError(http.StatusInternalServerError),
			want:        ApplyResult{Refound: []string{"book-new"}, Failed: failedCreate},
			wantWrites:  []string{"create book-new"},
			wantRefinds: []string{"book-new"},
		},
		{
			name:        "照合し直しの読み取りが 401 で失敗したらその時点で exitcode.Notion の error を返す",
			books:       []domain.Book{{ID: "book-new", ASIN: "ASIN-new"}, {ID: "book-next", ASIN: "ASIN-next"}},
			createErrs:  []error{unknown},
			refindErr:   notionStatusError(http.StatusUnauthorized),
			want:        ApplyResult{Refound: []string{"book-new"}},
			wantWrites:  []string{"create book-new"},
			wantRefinds: []string{"book-new"},
			wantCode:    4,
			wantErr:     "sync: notion write failed book-new: HTTP 401 Unauthorized",
		},
		{
			name:        "照合し直しの読み取りが 403 で失敗したらその時点で exitcode.Notion の error を返す",
			books:       one,
			createErrs:  []error{unknown},
			refindErr:   notionStatusError(http.StatusForbidden),
			want:        ApplyResult{Refound: []string{"book-new"}},
			wantWrites:  []string{"create book-new"},
			wantRefinds: []string{"book-new"},
			wantCode:    4,
			wantErr:     "sync: notion write failed book-new: HTTP 403 Forbidden",
		},
		{
			name:        "照合し直しの読み取りが 404 で失敗したらその時点で exitcode.Notion の error を返す",
			books:       one,
			createErrs:  []error{unknown},
			refindErr:   notionStatusError(http.StatusNotFound),
			want:        ApplyResult{Refound: []string{"book-new"}},
			wantWrites:  []string{"create book-new"},
			wantRefinds: []string{"book-new"},
			wantCode:    4,
			wantErr:     "sync: notion write failed book-new: HTTP 404 Not Found",
		},
		{
			name:       "結果が分からない失敗に当たらない 400 では照合し直さない",
			books:      one,
			createErrs: []error{badRequest},
			want:       ApplyResult{Failed: failedCreate},
			wantWrites: []string{"create book-new"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := &fakeDestination{
				matches:      map[string]destination.BookMatch{"book-new": none, "book-next": none},
				createErrSeq: map[string][]error{"book-new": tt.createErrs},
				refindSeq:    map[string][]destination.BookMatch{"book-new": tt.refinds},
				refindErrs:   map[string]error{},
			}
			if tt.refindErr != nil {
				dst.refindErrs["book-new"] = tt.refindErr
			}
			src := &fakeSource{highlights: map[string][]domain.Highlight{
				"book-new": applyHighlights("a-1", "a-2", "a-3"), "book-next": applyHighlights("a-1", "a-2", "a-3"),
			}}
			waits := 0
			oldSleep, oldJitter := recheckSleep, recheckJitter
			recheckSleep = func(context.Context, time.Duration) error { waits++; return nil }
			recheckJitter = func() time.Duration { return 0 }
			t.Cleanup(func() { recheckSleep, recheckJitter = oldSleep, oldJitter })

			got, err := ApplyBooks(context.Background(), tt.books, src, dst, &fakeState{}, applyNow)
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
			if !slices.Equal(dst.refindCalls, tt.wantRefinds) {
				t.Errorf("refindCalls = %v, want %v", dst.refindCalls, tt.wantRefinds)
			}
			if !reflect.DeepEqual(dst.appends, tt.wantAppends) {
				t.Errorf("appends = %v, want %v", dst.appends, tt.wantAppends)
			}
			if waits != tt.wantWaits {
				t.Errorf("waits = %d, want %d", waits, tt.wantWaits)
			}
		})
	}
}
