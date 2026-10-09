package source

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
)

func date(year int, month time.Month, day int) *time.Time {
	t := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	return &t
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func formatBooks(books []domain.Book) string {
	var b strings.Builder
	for _, book := range books {
		at := "<nil>"
		if book.LastAnnotatedAt != nil {
			at = book.LastAnnotatedAt.Format(time.RFC3339)
		}
		fmt.Fprintf(&b, "{ID:%q ASIN:%q Title:%q Author:%q LastAnnotatedAt:%s} ", book.ID, book.ASIN, book.Title, book.Author, at)
	}
	return b.String()
}

func TestParseLibraryPage(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		want    LibraryPage
	}{
		{
			name:    "続きのある 1 ページ目",
			fixture: "library_first.html",
			want: LibraryPage{
				Books: []domain.Book{
					{ID: "AAAAAAAAA1", ASIN: "AAAAAAAAA1", Title: "合成の書名 A", Author: "合成 著者 A", LastAnnotatedAt: date(2026, time.October, 8)},
					{ID: "AAAAAAAAA2", ASIN: "AAAAAAAAA2", Title: "合成の書名 B", Author: "合成 著者 B", LastAnnotatedAt: date(2025, time.December, 31)},
				},
				NextToken: "aaaa9999AAAA%2Baaaa9999%3D",
			},
		},
		{
			name:    "続きのある 2 ページ目以降の断片",
			fixture: "library_next.html",
			want: LibraryPage{
				Books: []domain.Book{
					{ID: "AAAAAAAAA3", ASIN: "AAAAAAAAA3", Title: "合成の書名 C", Author: "合成 著者 C", LastAnnotatedAt: date(2026, time.March, 1)},
					{ID: "AAAAAAAAA4", ASIN: "AAAAAAAAA4", Title: "合成の書名 D", Author: "合成 著者 D"},
				},
				NextToken: "aaaa9999BBBB%2Baaaa9999%3D",
			},
		},
		{
			name:    "Book が 0 件でも続きのある断片",
			fixture: "library_empty.html",
			want:    LibraryPage{NextToken: "aaaa9999CCCC%2Baaaa9999%3D"},
		},
		{
			name:    "最後のページ",
			fixture: "library_last.html",
			want: LibraryPage{
				Books: []domain.Book{
					{ID: "AAAAAAAAA5", ASIN: "AAAAAAAAA5", Title: "合成の書名 F", Author: "合成 著者 F", LastAnnotatedAt: date(2026, time.January, 5)},
				},
			},
		},
		{
			name:    "ASIN の無い Book",
			fixture: "library_no_asin.html",
			want: LibraryPage{
				Books: []domain.Book{
					{ID: "b1-b2a5dc299814d91a43fac695591953db", Title: "合成の書名 ASIN なし", Author: "合成 著者 E", LastAnnotatedAt: date(2024, time.February, 29)},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseLibraryPage(readFixture(t, tt.fixture))
			if err != nil {
				t.Fatalf("ParseLibraryPage(%s) error = %v", tt.fixture, err)
			}
			if !reflect.DeepEqual(got.Books, tt.want.Books) {
				t.Errorf("ParseLibraryPage(%s).Books = %s, want %s", tt.fixture, formatBooks(got.Books), formatBooks(tt.want.Books))
			}
			if got.NextToken != tt.want.NextToken {
				t.Errorf("ParseLibraryPage(%s).NextToken = %q, want %q", tt.fixture, got.NextToken, tt.want.NextToken)
			}
		})
	}
}

func TestParseLibraryPageError(t *testing.T) {
	const (
		title  = "合成の書名 エラー"
		author = "合成 著者 エラー"
	)
	bookHTML := func(dateValue string) string {
		return `<div id="AAAAAAAAA1" class="a-row kp-notebook-library-each-book">` +
			`<h2 class="kp-notebook-searchable">合成の書名 A</h2><p class="kp-notebook-searchable">合成 著者 A</p>` +
			`<input type="hidden" id="kp-notebook-annotated-date-AAAAAAAAA1" value="2026年10月8日木曜日"></div>` +
			`<div id="AAAAAAAAA2" class="a-row kp-notebook-library-each-book">` +
			`<h2 class="kp-notebook-searchable">` + title + `</h2><p class="kp-notebook-searchable">` + author + `</p>` +
			`<input type="hidden" id="kp-notebook-annotated-date-AAAAAAAAA2" value="` + dateValue + `"></div>` +
			`<input type="hidden" class="kp-notebook-library-next-page-start" value="">`
	}
	tests := []struct {
		name    string
		html    string
		want    string
		notWant []string
	}{
		{
			name:    "月と日がゼロ埋め",
			html:    bookHTML("2026年01月05日月曜日"),
			want:    "source: library page: book 2: last annotated date is not in the YYYY年M月D日<曜日> form",
			notWant: []string{title, author, "2026年01月05日月曜日"},
		},
		{
			name:    "曜日が無い",
			html:    bookHTML("2026年10月8日"),
			want:    "source: library page: book 2: last annotated date is not in the YYYY年M月D日<曜日> form",
			notWant: []string{title, author, "2026年10月8日"},
		},
		{
			name:    ".com 系の英語表記",
			html:    bookHTML("Thursday October 8, 2026"),
			want:    "source: library page: book 2: last annotated date is not in the YYYY年M月D日<曜日> form",
			notWant: []string{title, author, "Thursday"},
		},
		{
			name:    "空の value",
			html:    bookHTML(""),
			want:    "source: library page: book 2: last annotated date is not in the YYYY年M月D日<曜日> form",
			notWant: []string{title, author},
		},
		{
			name:    "存在しない日付",
			html:    bookHTML("2026年2月30日月曜日"),
			want:    "source: library page: book 2: last annotated date is not a valid calendar date",
			notWant: []string{title, author, "2026年2月30日月曜日"},
		},
		{
			name:    "日付と合わない曜日",
			html:    bookHTML("2026年10月8日金曜日"),
			want:    "source: library page: book 2: last annotated date has a weekday that does not match the date",
			notWant: []string{title, author, "2026年10月8日金曜日"},
		},
		{
			name: "続きの token の input が無い",
			html: `<div id="AAAAAAAAA1" class="a-row kp-notebook-library-each-book">` +
				`<h2 class="kp-notebook-searchable">` + title + `</h2><p class="kp-notebook-searchable">` + author + `</p></div>`,
			want:    "source: library page: next page token input is missing",
			notWant: []string{title, author},
		},
		{
			name:    "HTML として解析できない",
			html:    strings.Repeat("<div>", 600) + title + author,
			want:    "source: library page: parse HTML: html: open stack of elements exceeds 512 nodes",
			notWant: []string{title, author},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseLibraryPage([]byte(tt.html))
			if err == nil {
				t.Fatal("ParseLibraryPage() error = nil, want error")
			}
			msg := err.Error()
			if msg != tt.want {
				t.Errorf("ParseLibraryPage() error = %q, want %q", msg, tt.want)
			}
			for _, s := range tt.notWant {
				if strings.Contains(msg, s) {
					t.Errorf("ParseLibraryPage() error = %q, want it not to contain %q", msg, s)
				}
			}
		})
	}
}
