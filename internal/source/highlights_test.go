package source

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
)

func TestParseHighlightsPage(t *testing.T) {
	const (
		idHighlight1 = "QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OkhJR0hMSUdIVDphMDAwMDAwMDAtMDAwMC00MDAwLTgwMDAtMDAwMDAwMDAwMDAx"
		idHighlight2 = "QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OTpISUdITElHSFQ6YTAwMDAwMDAwLTAwMDAtNDAwMC04MDAwLTAwMDAwMDAwMDAwMw"
		idHighlight3 = "QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OTk6SElHSExJR0hUOmFhYWFhYTk5OTk5OQ"
		idHighlight4 = "QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OkhJR0hMSUdIVDphMDAwMDAwMDAtMDAwMC00MDAwLTgwMDAtMDAwMDAwMDAwMDA2"
		idNoteOnly   = "QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OTpOT1RFOmEwMDAwMDAwMC0wMDAwLTQwMDAtODAwMC0wMDAwMDAwMDAwMDU"
	)
	tests := []struct {
		name    string
		fixture string
		want    HighlightsPage
	}{
		{
			name:    "続きのある 1 ページ目",
			fixture: "highlights_first.html",
			want: HighlightsPage{
				Entries: []HighlightEntry{
					{RawID: idHighlight1, Text: "合成の本文 1", Note: "合成の Note 1", Location: "99"},
					{RawID: idHighlight2, Text: "合成の本文 2", Location: "999"},
				},
				NextToken:         "aaaa9999BBBB%2Baaaa",
				ContentLimitState: "aaaa9999AAAA%2Baaaa",
			},
		},
		{
			name:    "コンテナの無い 2 ページ目以降の最後のページ",
			fixture: "highlights_next.html",
			want: HighlightsPage{
				Entries: []HighlightEntry{
					{RawID: idHighlight3, Text: "合成の本文 3", Location: "9999"},
					{RawID: idHighlight4, Text: "合成の本文 4", Note: "合成の Note 4", Location: "99999"},
				},
				ContentLimitState: "aaaa9999AAAA%2Baaaa",
			},
		},
		{
			name:    "Note だけの Highlight",
			fixture: "highlights_note_only.html",
			want: HighlightsPage{
				Entries: []HighlightEntry{
					{RawID: idNoteOnly, Note: "合成の Note だけ", Location: "999"},
				},
				ContentLimitState: "aaaa9999AAAA%2Baaaa",
			},
		},
		{
			name:    "本文が表示されない Highlight",
			fixture: "highlights_empty_text.html",
			want: HighlightsPage{
				Entries: []HighlightEntry{
					{RawID: idHighlight1, Location: "99"},
				},
				ContentLimitState: "aaaa9999AAAA%2Baaaa",
			},
		},
		{
			name:    "Highlight が 0 件",
			fixture: "highlights_zero.html",
			want:    HighlightsPage{ContentLimitState: "aaaa9999AAAA%2Baaaa"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseHighlightsPage(readFixture(t, tt.fixture))
			if err != nil {
				t.Fatalf("ParseHighlightsPage(%s) error = %v", tt.fixture, err)
			}
			if !reflect.DeepEqual(got.Entries, tt.want.Entries) {
				t.Errorf("ParseHighlightsPage(%s).Entries = %+v, want %+v", tt.fixture, got.Entries, tt.want.Entries)
			}
			if got.NextToken != tt.want.NextToken {
				t.Errorf("ParseHighlightsPage(%s).NextToken = %q, want %q", tt.fixture, got.NextToken, tt.want.NextToken)
			}
			if got.ContentLimitState != tt.want.ContentLimitState {
				t.Errorf("ParseHighlightsPage(%s).ContentLimitState = %q, want %q", tt.fixture, got.ContentLimitState, tt.want.ContentLimitState)
			}
		})
	}
}

func TestParseHighlightsPageError(t *testing.T) {
	const (
		text = "合成の本文 エラー"
		note = "合成の Note エラー"
	)
	tests := []struct {
		name     string
		html     string
		want     string
		wantCode int // 構造の異常は 7（SourceFormat）、HTML として読めないのは 1（種類なし）
		notWant  []string
	}{
		{
			name:     "HTML として解析できない",
			html:     strings.Repeat("<div>", 600) + `<span id="highlight">` + text + `</span><span id="note">` + note + `</span>`,
			want:     "source: highlights page: parse HTML: html: open stack of elements exceeds 512 nodes",
			wantCode: 1,
			notWant:  []string{text, note, "<span"},
		},
		{
			name:     "続きの token の input が無い",
			html:     string(readFixture(t, "highlights_no_next_token.html")),
			want:     "source: highlights page: next page token input is missing: unexpected notebook structure",
			wantCode: 7,
			notWant:  []string{text, note, "<input"},
		},
		{
			name:     "続きの token の input に value 属性が無い",
			html:     string(readFixture(t, "highlights_next_token_no_value.html")),
			want:     "source: highlights page: next page token input has no value attribute: unexpected notebook structure",
			wantCode: 7,
			notWant:  []string{text, note, "<input"},
		},
		{
			name:     "本文も Note も空で本文非表示の印も無い行",
			html:     string(readFixture(t, "highlights_empty_row.html")),
			want:     "source: highlights page: row 2: highlight row has neither text nor note: unexpected notebook structure",
			wantCode: 7,
			notWant:  []string{text, note, "<div", "QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OTpISUdITElHSFQ6YTAwMDAwMDAwLTAwMDAtNDAwMC04MDAwLTAwMDAwMDAwMDAwMw"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseHighlightsPage([]byte(tt.html))
			if err == nil {
				t.Fatal("ParseHighlightsPage() error = nil, want error")
			}
			if !reflect.DeepEqual(got, HighlightsPage{}) {
				t.Errorf("ParseHighlightsPage() = %+v, want %+v", got, HighlightsPage{})
			}
			msg := err.Error()
			if msg != tt.want {
				t.Errorf("ParseHighlightsPage() error = %q, want %q", msg, tt.want)
			}
			if got := exitcode.Code(err); got != tt.wantCode {
				t.Errorf("exitcode.Code(err) = %d, want %d", got, tt.wantCode)
			}
			if got, want := errors.Is(err, ErrSourceFormat), tt.wantCode == 7; got != want {
				t.Errorf("errors.Is(err, ErrSourceFormat) = %v, want %v", got, want)
			}
			for _, s := range tt.notWant {
				if strings.Contains(msg, s) {
					t.Errorf("ParseHighlightsPage() error = %q, want it not to contain %q", msg, s)
				}
			}
		})
	}
}
