package source

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
)

func TestLibraryURL(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  string
	}{
		{"1 ページ目", "", "https://read.amazon.co.jp/notebook"},
		{"続きのページ", "aaaa9999AAAA%2Baaaa9999%3D", "https://read.amazon.co.jp/notebook?library=list&token=aaaa9999AAAA%2Baaaa9999%3D"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := libraryURL(tt.token); got != tt.want {
				t.Errorf("libraryURL(%q) = %q, want %q", tt.token, got, tt.want)
			}
		})
	}
}

// fakeLibraryPage は token ごとのページの返し方。fixture が空でなければ testdata から読み、
// err が nil でなければそれを返し、cancel なら返す前に ctx を切る。
type fakeLibraryPage struct {
	fixture string
	html    string
	err     error
	cancel  bool
}

// fakeLibraryFetch は token ごとの fakeLibraryPage に答える fetchPage と、呼ばれた token の記録を返す。
// map に無い token で呼ばれたらテストを失敗させる。
func fakeLibraryFetch(t *testing.T, pages map[string]fakeLibraryPage, cancel context.CancelFunc) (fetchPage, *[]string) {
	t.Helper()
	var mu sync.Mutex
	calls := []string{}
	return func(ctx context.Context, token string) ([]byte, error) {
		mu.Lock()
		calls = append(calls, token)
		mu.Unlock()
		p, ok := pages[token]
		if !ok {
			t.Errorf("fetch(ctx, %q): token is not in the fake library pages", token)
			return nil, errors.New("token is not in the fake library pages")
		}
		if p.cancel {
			cancel()
		}
		if p.err != nil {
			return nil, p.err
		}
		if p.fixture != "" {
			return readFixture(t, p.fixture), nil
		}
		return []byte(p.html), nil
	}, &calls
}

func TestListBooks(t *testing.T) {
	tests := []struct {
		name      string
		pages     map[string]fakeLibraryPage
		want      []domain.Book
		wantCalls []string
	}{
		{
			name: "3 ページ",
			pages: map[string]fakeLibraryPage{
				"":                           {fixture: "library_first.html"},
				"aaaa9999AAAA%2Baaaa9999%3D": {fixture: "library_next.html"},
				"aaaa9999BBBB%2Baaaa9999%3D": {fixture: "library_last.html"},
			},
			want: []domain.Book{
				{ID: "AAAAAAAAA1", ASIN: "AAAAAAAAA1", Title: "合成の書名 A", Author: "合成 著者 A", LastAnnotatedAt: date(2026, time.October, 8)},
				{ID: "AAAAAAAAA2", ASIN: "AAAAAAAAA2", Title: "合成の書名 B", Author: "合成 著者 B", LastAnnotatedAt: date(2025, time.December, 31)},
				{ID: "AAAAAAAAA3", ASIN: "AAAAAAAAA3", Title: "合成の書名 C", Author: "合成 著者 C", LastAnnotatedAt: date(2026, time.March, 1)},
				{ID: "AAAAAAAAA4", ASIN: "AAAAAAAAA4", Title: "合成の書名 D", Author: "合成 著者 D"},
				{ID: "AAAAAAAAA5", ASIN: "AAAAAAAAA5", Title: "合成の書名 F", Author: "合成 著者 F", LastAnnotatedAt: date(2026, time.January, 5)},
			},
			wantCalls: []string{"", "aaaa9999AAAA%2Baaaa9999%3D", "aaaa9999BBBB%2Baaaa9999%3D"},
		},
		{
			name: "Book 0 件の途中ページでも止まらない",
			pages: map[string]fakeLibraryPage{
				"":                           {fixture: "library_first.html"},
				"aaaa9999AAAA%2Baaaa9999%3D": {fixture: "library_next.html"},
				"aaaa9999BBBB%2Baaaa9999%3D": {fixture: "library_empty.html"},
				"aaaa9999CCCC%2Baaaa9999%3D": {fixture: "library_last.html"},
			},
			want: []domain.Book{
				{ID: "AAAAAAAAA1", ASIN: "AAAAAAAAA1", Title: "合成の書名 A", Author: "合成 著者 A", LastAnnotatedAt: date(2026, time.October, 8)},
				{ID: "AAAAAAAAA2", ASIN: "AAAAAAAAA2", Title: "合成の書名 B", Author: "合成 著者 B", LastAnnotatedAt: date(2025, time.December, 31)},
				{ID: "AAAAAAAAA3", ASIN: "AAAAAAAAA3", Title: "合成の書名 C", Author: "合成 著者 C", LastAnnotatedAt: date(2026, time.March, 1)},
				{ID: "AAAAAAAAA4", ASIN: "AAAAAAAAA4", Title: "合成の書名 D", Author: "合成 著者 D"},
				{ID: "AAAAAAAAA5", ASIN: "AAAAAAAAA5", Title: "合成の書名 F", Author: "合成 著者 F", LastAnnotatedAt: date(2026, time.January, 5)},
			},
			wantCalls: []string{"", "aaaa9999AAAA%2Baaaa9999%3D", "aaaa9999BBBB%2Baaaa9999%3D", "aaaa9999CCCC%2Baaaa9999%3D"},
		},
		{
			name: "1 ページだけ",
			pages: map[string]fakeLibraryPage{
				"": {fixture: "library_last.html"},
			},
			want: []domain.Book{
				{ID: "AAAAAAAAA5", ASIN: "AAAAAAAAA5", Title: "合成の書名 F", Author: "合成 著者 F", LastAnnotatedAt: date(2026, time.January, 5)},
			},
			wantCalls: []string{""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fetch, calls := fakeLibraryFetch(t, tt.pages, cancel)
			got, err := listBooks(ctx, fetch)
			if err != nil {
				t.Fatalf("listBooks() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("listBooks() = %s, want %s", formatBooks(got), formatBooks(tt.want))
			}
			if !reflect.DeepEqual(*calls, tt.wantCalls) {
				t.Errorf("listBooks() calls = %q, want %q", *calls, tt.wantCalls)
			}
		})
	}
}

var errFakeLibraryFetch = errors.New("fake fetch failed")

func TestListBooksError(t *testing.T) {
	tests := []struct {
		name      string
		pages     map[string]fakeLibraryPage
		want      string
		wantIs    error
		wantCalls []string
	}{
		{
			name: "2 ページ目の取得に失敗",
			pages: map[string]fakeLibraryPage{
				"":                           {fixture: "library_first.html"},
				"aaaa9999AAAA%2Baaaa9999%3D": {err: errFakeLibraryFetch},
			},
			want:      "source: list books: page 2: fake fetch failed",
			wantIs:    errFakeLibraryFetch,
			wantCalls: []string{"", "aaaa9999AAAA%2Baaaa9999%3D"},
		},
		{
			name: "2 ページ目の解析に失敗",
			pages: map[string]fakeLibraryPage{
				"":                           {fixture: "library_first.html"},
				"aaaa9999AAAA%2Baaaa9999%3D": {html: "<p>broken</p>"},
			},
			want:      "source: list books: page 2: source: library page: next page token input is missing",
			wantIs:    errLibraryNextTokenMissing,
			wantCalls: []string{"", "aaaa9999AAAA%2Baaaa9999%3D"},
		},
		{
			name: "直前と同じ token",
			pages: map[string]fakeLibraryPage{
				"":                           {fixture: "library_first.html"},
				"aaaa9999AAAA%2Baaaa9999%3D": {html: `<input type="hidden" class="kp-notebook-library-next-page-start" value="aaaa9999AAAA%2Baaaa9999%3D">`},
			},
			want:      "source: list books: page 2: next page token was already used",
			wantIs:    errLibraryTokenRepeated,
			wantCalls: []string{"", "aaaa9999AAAA%2Baaaa9999%3D"},
		},
		{
			name: "2 つ前と同じ token",
			pages: map[string]fakeLibraryPage{
				"":                           {fixture: "library_first.html"},
				"aaaa9999AAAA%2Baaaa9999%3D": {fixture: "library_next.html"},
				"aaaa9999BBBB%2Baaaa9999%3D": {html: `<input type="hidden" class="kp-notebook-library-next-page-start" value="aaaa9999AAAA%2Baaaa9999%3D">`},
			},
			want:      "source: list books: page 3: next page token was already used",
			wantIs:    errLibraryTokenRepeated,
			wantCalls: []string{"", "aaaa9999AAAA%2Baaaa9999%3D", "aaaa9999BBBB%2Baaaa9999%3D"},
		},
		{
			name: "context が途中で切れる",
			pages: map[string]fakeLibraryPage{
				"":                           {fixture: "library_first.html"},
				"aaaa9999AAAA%2Baaaa9999%3D": {fixture: "library_next.html", cancel: true},
			},
			want:      "source: list books: page 3: context canceled",
			wantIs:    context.Canceled,
			wantCalls: []string{"", "aaaa9999AAAA%2Baaaa9999%3D"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fetch, calls := fakeLibraryFetch(t, tt.pages, cancel)
			got, err := listBooks(ctx, fetch)
			if err == nil {
				t.Fatal("listBooks() error = nil, want error")
			}
			if got != nil {
				t.Errorf("listBooks() books = %s, want nil", formatBooks(got))
			}
			if err.Error() != tt.want {
				t.Errorf("listBooks() error = %q, want %q", err.Error(), tt.want)
			}
			if !errors.Is(err, tt.wantIs) {
				t.Errorf("errors.Is(err, %v) = false, err = %v", tt.wantIs, err)
			}
			if strings.Contains(err.Error(), "aaaa9999") {
				t.Errorf("listBooks() error = %q, want no token", err.Error())
			}
			if !reflect.DeepEqual(*calls, tt.wantCalls) {
				t.Errorf("listBooks() calls = %q, want %q", *calls, tt.wantCalls)
			}
		})
	}
}

func TestListBooksChrome(t *testing.T) {
	chromePath(t)

	t.Run("最後のページまで辿り全 Book を返す", func(t *testing.T) {
		f := &fakeAmazon{
			library: map[string][]byte{
				"": readFixture(t, "library_first.html"),
				"library=list&token=aaaa9999AAAA%2Baaaa9999%3D": readFixture(t, "library_next.html"),
				"library=list&token=aaaa9999BBBB%2Baaaa9999%3D": readFixture(t, "library_last.html"),
			},
		}
		f.signedIn.Store(true)
		b := openFakeAmazon(t, f)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		got, err := b.ListBooks(ctx)
		if err != nil {
			t.Fatalf("ListBooks() error = %v", err)
		}
		want := []domain.Book{
			{ID: "AAAAAAAAA1", ASIN: "AAAAAAAAA1", Title: "合成の書名 A", Author: "合成 著者 A", LastAnnotatedAt: date(2026, time.October, 8)},
			{ID: "AAAAAAAAA2", ASIN: "AAAAAAAAA2", Title: "合成の書名 B", Author: "合成 著者 B", LastAnnotatedAt: date(2025, time.December, 31)},
			{ID: "AAAAAAAAA3", ASIN: "AAAAAAAAA3", Title: "合成の書名 C", Author: "合成 著者 C", LastAnnotatedAt: date(2026, time.March, 1)},
			{ID: "AAAAAAAAA4", ASIN: "AAAAAAAAA4", Title: "合成の書名 D", Author: "合成 著者 D"},
			{ID: "AAAAAAAAA5", ASIN: "AAAAAAAAA5", Title: "合成の書名 F", Author: "合成 著者 F", LastAnnotatedAt: date(2026, time.January, 5)},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ListBooks() = %s, want %s", formatBooks(got), formatBooks(want))
		}
		wantQueries := []string{"", "library=list&token=aaaa9999AAAA%2Baaaa9999%3D", "library=list&token=aaaa9999BBBB%2Baaaa9999%3D"}
		if queries := f.libraryQueries(); !reflect.DeepEqual(queries, wantQueries) {
			t.Errorf("queries = %q, want %q", queries, wantQueries)
		}
	})
}
