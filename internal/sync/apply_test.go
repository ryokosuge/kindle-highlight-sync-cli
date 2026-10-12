package sync

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	saveCtxs  []error // SaveHighlights が受け取った context の Err()
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
				Failed:   []BookFailure{{BookID: "book-exact", Kind: BookFailureUpdate}},
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
			name:    "1 冊だけの構造の異常で Highlight の取得が失敗したら残りの Book を続け、その Book には追記しない",
			srcErrs: map[string]error{"book-exact": bookFormatError},
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
			name:    "Highlight の取得がタイムアウトしたら残りの Book を続け、その Book ID が失敗に残る",
			srcErrs: map[string]error{"book-exact": context.DeadlineExceeded},
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
			name:    "包まれたタイムアウトで Highlight の取得が失敗しても残りの Book を続ける",
			srcErrs: map[string]error{"book-adopt": exitcode.New(exitcode.Browser, fmt.Errorf("source: highlights page: %w", context.DeadlineExceeded))},
			want: ApplyResult{
				Created:  []string{"book-new"},
				Adopted:  []string{"book-adopt"},
				Exact:    []string{"book-exact"},
				Skipped:  []string{"book-ambiguous"},
				Failed:   []BookFailure{{BookID: "book-adopt", Kind: BookFailureFetch}},
				Appended: 6,
			},
			wantAppends:   map[string][]string{"page-book-new": {"a-1", "a-2", "a-3"}, "page-exact": {"a-1", "a-2", "a-3"}},
			wantListCalls: []string{"page-exact"},
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
			want:          ApplyResult{Exact: []string{"book-exact"}, Failed: []BookFailure{{BookID: "book-exact", Kind: BookFailureAppend}}, Appended: 2},
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
