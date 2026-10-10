package source

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
)

func TestHighlightsURL(t *testing.T) {
	tests := []struct {
		name              string
		asin              string
		token             string
		contentLimitState string
		want              string
	}{
		{"1 ページ目", "AAAAAAAAA1", "", "", "https://read.amazon.co.jp/notebook?asin=AAAAAAAAA1"},
		{"contentLimitState だけ", "AAAAAAAAA1", "", "aaaa9999AAAA%2Baaaa", "https://read.amazon.co.jp/notebook?asin=AAAAAAAAA1&contentLimitState=aaaa9999AAAA%2Baaaa"},
		{"contentLimitState と token", "AAAAAAAAA1", "aaaa9999BBBB%2Baaaa", "aaaa9999AAAA%2Baaaa", "https://read.amazon.co.jp/notebook?asin=AAAAAAAAA1&contentLimitState=aaaa9999AAAA%2Baaaa&token=aaaa9999BBBB%2Baaaa"},
		{"英数字だけの contentLimitState", "AAAAAAAAA1", "aaaa9999BBBB%2Baaaa", "aaaa9999AAAA", "https://read.amazon.co.jp/notebook?asin=AAAAAAAAA1&contentLimitState=aaaa9999AAAA&token=aaaa9999BBBB%2Baaaa"},
		{"エスケープが要る ASIN", "A B&C=1", "", "", "https://read.amazon.co.jp/notebook?asin=A+B%26C%3D1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := highlightsURL(tt.asin, tt.token, tt.contentLimitState); got != tt.want {
				t.Errorf("highlightsURL(%q, %q, %q) = %q, want %q", tt.asin, tt.token, tt.contentLimitState, got, tt.want)
			}
		})
	}
}

// highlightsCall は fakeHighlightsFetch に渡された引数 1 回分。
type highlightsCall struct {
	asin              string
	token             string
	contentLimitState string
}

// fakeHighlightsPage は token ごとのページの返し方。fixture が空でなければ testdata から読み、
// err が nil でなければそれを返し、cancel なら返す前に ctx を切る。
type fakeHighlightsPage struct {
	fixture string
	html    string
	err     error
	cancel  bool
}

// fakeHighlightsFetch は token ごとの fakeHighlightsPage に答える fetchHighlightsPage と、呼ばれた引数の記録を返す。
// map に無い token や、同じ token で 2 度呼ばれたらテストを失敗させる。
func fakeHighlightsFetch(t *testing.T, pages map[string]fakeHighlightsPage, cancel context.CancelFunc) (fetchHighlightsPage, *[]highlightsCall) {
	t.Helper()
	var mu sync.Mutex
	calls := []highlightsCall{}
	return func(ctx context.Context, asin, token, contentLimitState string) ([]byte, error) {
		mu.Lock()
		repeated := slices.ContainsFunc(calls, func(c highlightsCall) bool { return c.token == token })
		calls = append(calls, highlightsCall{asin: asin, token: token, contentLimitState: contentLimitState})
		mu.Unlock()
		if repeated {
			t.Errorf("fetch(ctx, %q, %q, %q): called twice with the same token", asin, token, contentLimitState)
			return nil, errors.New("called twice with the same token")
		}
		p, ok := pages[token]
		if !ok {
			t.Errorf("fetch(ctx, %q, %q, %q): token is not in the fake highlights pages", asin, token, contentLimitState)
			return nil, errors.New("token is not in the fake highlights pages")
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

// highlightsMiddleHTML は content-limit-state の無い途中ページの断片。
const highlightsMiddleHTML = `<input type="hidden" class="kp-notebook-annotations-next-page-start" value="aaaa9999CCCC%2Baaaa"><div id="row-middle" class="a-row a-spacing-base"><input type="hidden" id="kp-annotation-location" value="5000"><div class="kp-notebook-highlight"><span id="highlight">合成の本文 中</span></div></div>`

// highlightsMiddleCLSHTML は content-limit-state を持つ途中ページの断片。
const highlightsMiddleCLSHTML = `<input type="hidden" class="kp-notebook-content-limit-state" value="aaaa9999DDDD"><input type="hidden" class="kp-notebook-annotations-next-page-start" value="aaaa9999CCCC%2Baaaa"><div id="row-middle" class="a-row a-spacing-base"><input type="hidden" id="kp-annotation-location" value="5000"><div class="kp-notebook-highlight"><span id="highlight">合成の本文 中</span></div></div>`

func TestListHighlightEntries(t *testing.T) {
	const asin = "AAAAAAAAA1"
	threePages := []HighlightEntry{
		{RawID: "QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OkhJR0hMSUdIVDphMDAwMDAwMDAtMDAwMC00MDAwLTgwMDAtMDAwMDAwMDAwMDAx", Text: "合成の本文 1", Note: "合成の Note 1", Location: "99"},
		{RawID: "QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OTpISUdITElHSFQ6YTAwMDAwMDAwLTAwMDAtNDAwMC04MDAwLTAwMDAwMDAwMDAwMw", Text: "合成の本文 2", Location: "999"},
		{RawID: "row-middle", Text: "合成の本文 中", Location: "5000"},
		{RawID: "QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OTk6SElHSExJR0hUOmFhYWFhYTk5OTk5OQ", Text: "合成の本文 3", Location: "9999"},
		{RawID: "QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OkhJR0hMSUdIVDphMDAwMDAwMDAtMDAwMC00MDAwLTgwMDAtMDAwMDAwMDAwMDA2", Text: "合成の本文 4", Note: "合成の Note 4", Location: "99999"},
	}
	tests := []struct {
		name      string
		pages     map[string]fakeHighlightsPage
		want      []HighlightEntry
		wantCalls []highlightsCall
	}{
		{
			name: "3 ページ",
			pages: map[string]fakeHighlightsPage{
				"":                    {fixture: "highlights_first.html"},
				"aaaa9999BBBB%2Baaaa": {html: highlightsMiddleHTML},
				"aaaa9999CCCC%2Baaaa": {fixture: "highlights_next.html"},
			},
			want: threePages,
			wantCalls: []highlightsCall{
				{asin: asin},
				{asin: asin, token: "aaaa9999BBBB%2Baaaa", contentLimitState: "aaaa9999AAAA%2Baaaa"},
				{asin: asin, token: "aaaa9999CCCC%2Baaaa", contentLimitState: "aaaa9999AAAA%2Baaaa"},
			},
		},
		{
			name: "後のページの ContentLimitState で更新する",
			pages: map[string]fakeHighlightsPage{
				"":                    {fixture: "highlights_first.html"},
				"aaaa9999BBBB%2Baaaa": {html: highlightsMiddleCLSHTML},
				"aaaa9999CCCC%2Baaaa": {fixture: "highlights_next.html"},
			},
			want: threePages,
			wantCalls: []highlightsCall{
				{asin: asin},
				{asin: asin, token: "aaaa9999BBBB%2Baaaa", contentLimitState: "aaaa9999AAAA%2Baaaa"},
				{asin: asin, token: "aaaa9999CCCC%2Baaaa", contentLimitState: "aaaa9999DDDD"},
			},
		},
		{
			name: "1 ページだけ",
			pages: map[string]fakeHighlightsPage{
				"": {fixture: "highlights_next.html"},
			},
			want: []HighlightEntry{
				{RawID: "QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OTk6SElHSExJR0hUOmFhYWFhYTk5OTk5OQ", Text: "合成の本文 3", Location: "9999"},
				{RawID: "QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OkhJR0hMSUdIVDphMDAwMDAwMDAtMDAwMC00MDAwLTgwMDAtMDAwMDAwMDAwMDA2", Text: "合成の本文 4", Note: "合成の Note 4", Location: "99999"},
			},
			wantCalls: []highlightsCall{
				{asin: asin},
			},
		},
		{
			name: "Highlight が 0 件",
			pages: map[string]fakeHighlightsPage{
				"": {fixture: "highlights_zero.html"},
			},
			wantCalls: []highlightsCall{
				{asin: asin},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fetch, calls := fakeHighlightsFetch(t, tt.pages, cancel)
			got, err := listHighlightEntries(ctx, asin, fetch)
			if err != nil {
				t.Fatalf("listHighlightEntries() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("listHighlightEntries() = %+v, want %+v", got, tt.want)
			}
			if !reflect.DeepEqual(*calls, tt.wantCalls) {
				t.Errorf("listHighlightEntries() calls = %+v, want %+v", *calls, tt.wantCalls)
			}
		})
	}
}

func TestListHighlights(t *testing.T) {
	const asin = "AAAAAAAAA1"
	book := domain.Book{ID: asin, ASIN: asin, Title: "合成の書名 A"}
	pages := map[string]fakeHighlightsPage{
		"":                    {html: withNextToken(t, "highlights_empty_text.html", "aaaa9999BBBB%2Baaaa")},
		"aaaa9999BBBB%2Baaaa": {html: withNextToken(t, "highlights_note_only.html", "aaaa9999CCCC%2Baaaa")},
		"aaaa9999CCCC%2Baaaa": {fixture: "highlights_next.html"},
	}
	fetch, calls := fakeHighlightsFetch(t, pages, nil)
	got, err := listHighlights(context.Background(), book, fetch)
	if err != nil {
		t.Fatalf("listHighlights() error = %v", err)
	}
	want := []domain.Highlight{
		{ID: "a-highlight-a00000000-0000-4000-8000-000000000001", BookID: book.ID, Text: "", Note: "", Location: "99", Page: ""},
		{ID: "a-note-a00000000-0000-4000-8000-000000000005", BookID: book.ID, Text: "", Note: "合成の Note だけ", Location: "999", Page: ""},
		{ID: "a-highlight-aaaaaa999999", BookID: book.ID, Text: "合成の本文 3", Note: "", Location: "9999", Page: ""},
		{ID: "a-highlight-a00000000-0000-4000-8000-000000000006", BookID: book.ID, Text: "合成の本文 4", Note: "合成の Note 4", Location: "99999", Page: ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("listHighlights() = %+v, want %+v", got, want)
	}
	for i, h := range got {
		if got := domain.IsAdoptedHighlightID(h.ID); !got {
			t.Errorf("domain.IsAdoptedHighlightID(got[%d].ID) = %v, want %v", i, got, true)
		}
	}
	wantCalls := []highlightsCall{
		{asin: asin},
		{asin: asin, token: "aaaa9999BBBB%2Baaaa", contentLimitState: "aaaa9999AAAA%2Baaaa"},
		{asin: asin, token: "aaaa9999CCCC%2Baaaa", contentLimitState: "aaaa9999AAAA%2Baaaa"},
	}
	if !reflect.DeepEqual(*calls, wantCalls) {
		t.Errorf("listHighlights() calls = %+v, want %+v", *calls, wantCalls)
	}
}

func withNextToken(t *testing.T, fixture, token string) string {
	t.Helper()
	const marker = `class="kp-notebook-annotations-next-page-start" value=""`
	html := string(readFixture(t, fixture))
	if got := strings.Count(html, marker); got != 1 {
		t.Fatalf("strings.Count(%q, marker) = %d, want %d", fixture, got, 1)
	}
	return strings.Replace(html, marker, `class="kp-notebook-annotations-next-page-start" value="`+token+`"`, 1)
}

func TestListHighlightsIDError(t *testing.T) {
	const asin = "AAAAAAAAA1"
	tests := []struct {
		name    string
		book    domain.Book
		pages   map[string]fakeHighlightsPage
		notWant []string
	}{
		{
			name: "途中のページの行 id が base64 でない",
			book: domain.Book{ID: asin, ASIN: asin},
			pages: map[string]fakeHighlightsPage{
				"":                    {fixture: "highlights_first.html"},
				"aaaa9999BBBB%2Baaaa": {html: highlightsMiddleHTML},
				"aaaa9999CCCC%2Baaaa": {fixture: "highlights_next.html"},
			},
			notWant: []string{
				"QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OkhJR0hMSUdIVDphMDAwMDAwMDAtMDAwMC00MDAwLTgwMDAtMDAwMDAwMDAwMDAx",
				"QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OTpISUdITElHSFQ6YTAwMDAwMDAwLTAwMDAtNDAwMC04MDAwLTAwMDAwMDAwMDAwMw",
				"row-middle",
				"A9AAAA99A99AA",
			},
		},
		{
			name: "ASIN が違う",
			book: domain.Book{ID: "AAAAAAAAA2", ASIN: "AAAAAAAAA2"},
			pages: map[string]fakeHighlightsPage{
				"": {fixture: "highlights_next.html"},
			},
			notWant: []string{
				"QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OTk6SElHSExJR0hUOmFhYWFhYTk5OTk5OQ",
				"QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OkhJR0hMSUdIVDphMDAwMDAwMDAtMDAwMC00MDAwLTgwMDAtMDAwMDAwMDAwMDA2",
				"A9AAAA99A99AA",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetch, _ := fakeHighlightsFetch(t, tt.pages, nil)
			got, err := listHighlights(context.Background(), tt.book, fetch)
			if got != nil {
				t.Errorf("listHighlights() = %+v, want nil", got)
			}
			if err == nil {
				t.Fatal("listHighlights() error = nil, want error")
			}
			if !errors.Is(err, domain.ErrMissingAnnotationID) {
				t.Errorf("errors.Is(err, domain.ErrMissingAnnotationID) = %v, want %v", errors.Is(err, domain.ErrMissingAnnotationID), true)
			}
			for _, value := range tt.notWant {
				if got := strings.Contains(err.Error(), value); got {
					t.Errorf("strings.Contains(err.Error(), %q) = %v, want %v", value, got, false)
				}
			}
		})
	}
}

func TestListHighlightsFetchError(t *testing.T) {
	const asin = "AAAAAAAAA1"
	browserErr := exitcode.New(exitcode.Browser, errors.New("chrome crashed"))
	plainErr := errors.New("fetch failed")
	tests := []struct {
		name     string
		fetchErr error
		wantCode int
		wantIs   error
	}{
		{name: "Browser の種類を保つ", fetchErr: browserErr, wantCode: int(exitcode.Browser), wantIs: browserErr},
		{name: "種類のないエラーは General", fetchErr: plainErr, wantCode: int(exitcode.General), wantIs: plainErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			book := domain.Book{ID: asin, ASIN: asin}
			fetch, _ := fakeHighlightsFetch(t, map[string]fakeHighlightsPage{"": {err: tt.fetchErr}}, nil)
			got, err := listHighlights(context.Background(), book, fetch)
			if got != nil {
				t.Errorf("listHighlights() = %+v, want nil", got)
			}
			if err == nil {
				t.Fatal("listHighlights() error = nil, want error")
			}
			if got := exitcode.Code(err); got != tt.wantCode {
				t.Errorf("exitcode.Code(err) = %d, want %d", got, tt.wantCode)
			}
			if !errors.Is(err, tt.wantIs) {
				t.Errorf("errors.Is(err, tt.wantIs) = %v, want %v", errors.Is(err, tt.wantIs), true)
			}
		})
	}
}

func TestListHighlightsDuplicateID(t *testing.T) {
	const asin = "AAAAAAAAA1"
	book := domain.Book{ID: asin, ASIN: asin}
	fetch, _ := fakeHighlightsFetch(t, map[string]fakeHighlightsPage{
		"":                    {fixture: "highlights_first.html"},
		"aaaa9999BBBB%2Baaaa": {fixture: "highlights_empty_text.html"},
	}, nil)
	got, err := listHighlights(context.Background(), book, fetch)
	if got != nil {
		t.Errorf("listHighlights() = %+v, want nil", got)
	}
	if err == nil {
		t.Fatal("listHighlights() error = nil, want error")
	}
	if !errors.Is(err, errHighlightIDDuplicated) {
		t.Errorf("errors.Is(err, errHighlightIDDuplicated) = %v, want %v", errors.Is(err, errHighlightIDDuplicated), true)
	}
}

var errFakeHighlightsFetch = errors.New("fake fetch failed")

func TestListHighlightEntriesError(t *testing.T) {
	tests := []struct {
		name      string
		asin      string
		pages     map[string]fakeHighlightsPage
		want      string
		wantIs    error
		wantCalls []highlightsCall
	}{
		{
			name: "2 ページ目の取得に失敗",
			asin: "AAAAAAAAA1",
			pages: map[string]fakeHighlightsPage{
				"":                    {fixture: "highlights_first.html"},
				"aaaa9999BBBB%2Baaaa": {err: errFakeHighlightsFetch},
			},
			want:   "source: list highlights AAAAAAAAA1: page 2: fake fetch failed",
			wantIs: errFakeHighlightsFetch,
			wantCalls: []highlightsCall{
				{asin: "AAAAAAAAA1"},
				{asin: "AAAAAAAAA1", token: "aaaa9999BBBB%2Baaaa", contentLimitState: "aaaa9999AAAA%2Baaaa"},
			},
		},
		{
			name: "2 ページ目の解析に失敗",
			asin: "AAAAAAAAA1",
			pages: map[string]fakeHighlightsPage{
				"":                    {fixture: "highlights_first.html"},
				"aaaa9999BBBB%2Baaaa": {html: "<p>broken</p>"},
			},
			want:   "source: list highlights AAAAAAAAA1: page 2: source: highlights page: next page token input is missing",
			wantIs: errHighlightsNextTokenMissing,
			wantCalls: []highlightsCall{
				{asin: "AAAAAAAAA1"},
				{asin: "AAAAAAAAA1", token: "aaaa9999BBBB%2Baaaa", contentLimitState: "aaaa9999AAAA%2Baaaa"},
			},
		},
		{
			name: "直前と同じ token",
			asin: "AAAAAAAAA1",
			pages: map[string]fakeHighlightsPage{
				"":                    {fixture: "highlights_first.html"},
				"aaaa9999BBBB%2Baaaa": {html: `<input type="hidden" class="kp-notebook-annotations-next-page-start" value="aaaa9999BBBB%2Baaaa">`},
			},
			want:   "source: list highlights AAAAAAAAA1: page 2: next page token was already used",
			wantIs: errHighlightsTokenRepeated,
			wantCalls: []highlightsCall{
				{asin: "AAAAAAAAA1"},
				{asin: "AAAAAAAAA1", token: "aaaa9999BBBB%2Baaaa", contentLimitState: "aaaa9999AAAA%2Baaaa"},
			},
		},
		{
			name: "2 つ前と同じ token",
			asin: "AAAAAAAAA1",
			pages: map[string]fakeHighlightsPage{
				"":                    {fixture: "highlights_first.html"},
				"aaaa9999BBBB%2Baaaa": {html: highlightsMiddleHTML},
				"aaaa9999CCCC%2Baaaa": {html: `<input type="hidden" class="kp-notebook-annotations-next-page-start" value="aaaa9999BBBB%2Baaaa">`},
			},
			want:   "source: list highlights AAAAAAAAA1: page 3: next page token was already used",
			wantIs: errHighlightsTokenRepeated,
			wantCalls: []highlightsCall{
				{asin: "AAAAAAAAA1"},
				{asin: "AAAAAAAAA1", token: "aaaa9999BBBB%2Baaaa", contentLimitState: "aaaa9999AAAA%2Baaaa"},
				{asin: "AAAAAAAAA1", token: "aaaa9999CCCC%2Baaaa", contentLimitState: "aaaa9999AAAA%2Baaaa"},
			},
		},
		{
			name: "context が途中で切れる",
			asin: "AAAAAAAAA1",
			pages: map[string]fakeHighlightsPage{
				"":                    {fixture: "highlights_first.html"},
				"aaaa9999BBBB%2Baaaa": {html: highlightsMiddleHTML, cancel: true},
			},
			want:   "source: list highlights AAAAAAAAA1: page 3: context canceled",
			wantIs: context.Canceled,
			wantCalls: []highlightsCall{
				{asin: "AAAAAAAAA1"},
				{asin: "AAAAAAAAA1", token: "aaaa9999BBBB%2Baaaa", contentLimitState: "aaaa9999AAAA%2Baaaa"},
			},
		},
		{
			name:      "ASIN が空",
			asin:      "",
			pages:     map[string]fakeHighlightsPage{},
			want:      "source: list highlights: book has no ASIN",
			wantIs:    errHighlightsASINEmpty,
			wantCalls: []highlightsCall{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fetch, calls := fakeHighlightsFetch(t, tt.pages, cancel)
			got, err := listHighlightEntries(ctx, tt.asin, fetch)
			if err == nil {
				t.Fatal("listHighlightEntries() error = nil, want error")
			}
			if got != nil {
				t.Errorf("listHighlightEntries() entries = %+v, want nil", got)
			}
			if err.Error() != tt.want {
				t.Errorf("listHighlightEntries() error = %q, want %q", err.Error(), tt.want)
			}
			if !errors.Is(err, tt.wantIs) {
				t.Errorf("errors.Is(err, %v) = false, err = %v", tt.wantIs, err)
			}
			for _, leak := range []string{"aaaa9999", "合成の本文", "合成の Note", "合成の書名"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("listHighlightEntries() error = %q, want no %q", err.Error(), leak)
				}
			}
			if !reflect.DeepEqual(*calls, tt.wantCalls) {
				t.Errorf("listHighlightEntries() calls = %+v, want %+v", *calls, tt.wantCalls)
			}
		})
	}
}

func TestListHighlightEntriesChrome(t *testing.T) {
	chromePath(t)

	t.Run("最後のページまで辿り全 Highlight を返す", func(t *testing.T) {
		f := &fakeAmazon{
			library: map[string][]byte{
				"asin=AAAAAAAAA1": readFixture(t, "highlights_first.html"),
				"asin=AAAAAAAAA1&contentLimitState=aaaa9999AAAA%2Baaaa&token=aaaa9999BBBB%2Baaaa": readFixture(t, "highlights_next.html"),
			},
		}
		f.signedIn.Store(true)
		b := openFakeAmazon(t, f)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		got, err := listHighlightEntries(ctx, "AAAAAAAAA1", b.fetchHighlightsPage)
		if err != nil {
			t.Fatalf("listHighlightEntries() error = %v", err)
		}
		want := []HighlightEntry{
			{RawID: "QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OkhJR0hMSUdIVDphMDAwMDAwMDAtMDAwMC00MDAwLTgwMDAtMDAwMDAwMDAwMDAx", Text: "合成の本文 1", Note: "合成の Note 1", Location: "99"},
			{RawID: "QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OTpISUdITElHSFQ6YTAwMDAwMDAwLTAwMDAtNDAwMC04MDAwLTAwMDAwMDAwMDAwMw", Text: "合成の本文 2", Location: "999"},
			{RawID: "QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OTk6SElHSExJR0hUOmFhYWFhYTk5OTk5OQ", Text: "合成の本文 3", Location: "9999"},
			{RawID: "QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OkhJR0hMSUdIVDphMDAwMDAwMDAtMDAwMC00MDAwLTgwMDAtMDAwMDAwMDAwMDA2", Text: "合成の本文 4", Note: "合成の Note 4", Location: "99999"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("listHighlightEntries() = %+v, want %+v", got, want)
		}
		wantQueries := []string{
			"asin=AAAAAAAAA1",
			"asin=AAAAAAAAA1&contentLimitState=aaaa9999AAAA%2Baaaa&token=aaaa9999BBBB%2Baaaa",
		}
		if queries := f.libraryQueries(); !reflect.DeepEqual(queries, wantQueries) {
			t.Errorf("queries = %q, want %q", queries, wantQueries)
		}
	})
}
