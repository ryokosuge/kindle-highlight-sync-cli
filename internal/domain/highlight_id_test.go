package domain

import (
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"testing"
)

var (
	highlightIDPattern       = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
	highlightIDMarkerPattern = regexp.MustCompile(`khs:([A-Za-z0-9_-]+)`)
)

func TestHighlightIDPrefix(t *testing.T) {
	if HighlightIDPrefix != "a-" {
		t.Errorf("HighlightIDPrefix = %q, want %q", HighlightIDPrefix, "a-")
	}
}

func TestIsAdoptedHighlightID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{name: "採用方式の a- で始まる note の ID", id: "a-note-0123abcd-4567-89ab-cdef-0123456789ab", want: true},
		{name: "採用方式の a- で始まる highlight の ID", id: "a-highlight-ABC123def4567", want: true},
		{name: "もう一方の方式の h1- は採用方式でない", id: "h1-abc", want: false},
		{name: "未知の prefix h2- は採用方式でない", id: "h2-abc", want: false},
		{name: "- の無い値は採用方式でない", id: "abc", want: false},
		{name: "空文字は採用方式でない", id: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAdoptedHighlightID(tt.id); got != tt.want {
				t.Errorf("IsAdoptedHighlightID(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}

func TestHighlightIDPrefixOf(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want string
	}{
		{name: "a- 方式の ID は a- を返す", id: "a-xyz", want: "a-"},
		{name: "a-<type>-<annotation> は最初の - までの a- を返す", id: "a-note-abc", want: "a-"},
		{name: "h1- 方式の ID は h1- を返す", id: "h1-abc", want: "h1-"},
		{name: "未知の prefix はそのまま返す", id: "h2-abc", want: "h2-"},
		{name: "- の無い値は空文字を返す", id: "abc", want: ""},
		{name: "空文字は空文字を返す", id: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HighlightIDPrefixOf(tt.id); got != tt.want {
				t.Errorf("HighlightIDPrefixOf(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestNewHighlightID(t *testing.T) {
	const asin = "AAAAAAAAA1"
	tests := []struct {
		name     string
		rawID    string
		account  string
		position string
		want     string
	}{
		{
			name:     "Highlight だけの行",
			rawID:    "QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OTpISUdITElHSFQ6YTAwMDAwMDAwLTAwMDAtNDAwMC04MDAwLTAwMDAwMDAwMDAwMw",
			account:  "A9AAAA99A99AA",
			position: "999999",
			want:     "a-highlight-a00000000-0000-4000-8000-000000000003",
		},
		{
			name:     "Highlight と Note の行",
			rawID:    "QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OkhJR0hMSUdIVDphMDAwMDAwMDAtMDAwMC00MDAwLTgwMDAtMDAwMDAwMDAwMDAx",
			account:  "A9AAAA99A99AA",
			position: "99999",
			want:     "a-highlight-a00000000-0000-4000-8000-000000000001",
		},
		{
			name:     "Note だけの行",
			rawID:    "QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OTpOT1RFOmEwMDAwMDAwMC0wMDAwLTQwMDAtODAwMC0wMDAwMDAwMDAwMDU",
			account:  "A9AAAA99A99AA",
			position: "999999",
			want:     "a-note-a00000000-0000-4000-8000-000000000005",
		},
		{
			name:     "ハイフンの無い短い形",
			rawID:    "QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OTk6SElHSExJR0hUOmFhYWFhYTk5OTk5OQ",
			account:  "A9AAAA99A99AA",
			position: "9999999",
			want:     "a-highlight-aaaaaa999999",
		},
		{
			name:     "大文字・小文字を変えない",
			rawID:    "QTlBQUFBOTlBOTlBQTpBQUFBQUFBQUExOjk5OTk5OTk6Tk9URTphQWFhOUFhQWFhQWE",
			account:  "A9AAAA99A99AA",
			position: "9999999",
			want:     "a-note-aAaa9AaAaaAa",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewHighlightID(tt.rawID, asin)
			if err != nil {
				t.Fatalf("NewHighlightID(%q, %q) error = %v", tt.rawID, asin, err)
			}
			if got != tt.want {
				t.Errorf("NewHighlightID(%q, %q) = %q, want %q", tt.rawID, asin, got, tt.want)
			}
			again, err := NewHighlightID(tt.rawID, asin)
			if err != nil {
				t.Fatalf("NewHighlightID(%q, %q) second call error = %v", tt.rawID, asin, err)
			}
			if again != got {
				t.Errorf("NewHighlightID(%q, %q) second call = %q, want %q", tt.rawID, asin, again, got)
			}
			if got := IsAdoptedHighlightID(got); !got {
				t.Errorf("IsAdoptedHighlightID(%q) = %v, want %v", tt.want, got, true)
			}
			if got := highlightIDPattern.MatchString(tt.want); !got {
				t.Errorf("highlightIDPattern.MatchString(%q) = %v, want %v", tt.want, got, true)
			}
			if got := highlightIDMarkerPattern.FindStringSubmatch("khs:" + tt.want); len(got) != 2 || got[1] != tt.want {
				t.Errorf("highlightIDMarkerPattern.FindStringSubmatch(%q) = %v, want capture %q", "khs:"+tt.want, got, tt.want)
			}
			for _, value := range []string{tt.account, tt.position, asin} {
				if got := strings.Contains(tt.want, value); got {
					t.Errorf("strings.Contains(%q, %q) = %v, want %v", tt.want, value, got, false)
				}
			}
		})
	}
}

func TestNewHighlightIDError(t *testing.T) {
	const (
		account         = "A9AAAA99A99AA"
		rowASIN         = "AAAAAAAAA1"
		position        = "99999"
		validAnnotation = "a00000000-0000-4000-8000-000000000001"
	)
	tests := []struct {
		name             string
		rawID            string
		asin             string
		standardEncoding bool
	}{
		{name: "行 id が空", asin: rowASIN},
		{name: "base64 としてデコードできない", rawID: "@@@@", asin: rowASIN},
		{
			name:             "パディング付きの base64",
			rawID:            base64.StdEncoding.EncodeToString([]byte(strings.Join([]string{account, rowASIN, "9999", "HIGHLIGHT", validAnnotation}, ":"))),
			asin:             rowASIN,
			standardEncoding: true,
		},
		{name: "4 要素", rawID: encodeRowID(account, rowASIN, position, "HIGHLIGHT"), asin: rowASIN},
		{name: "6 要素", rawID: encodeRowID(account, rowASIN, position, "HIGHLIGHT", validAnnotation, "extra"), asin: rowASIN},
		{name: "ASIN が違う", rawID: encodeRowID(account, rowASIN, position, "HIGHLIGHT", validAnnotation), asin: "AAAAAAAAA2"},
		{name: "type が BOOKMARK", rawID: encodeRowID(account, rowASIN, position, "BOOKMARK", validAnnotation), asin: rowASIN},
		{name: "type が小文字の highlight", rawID: encodeRowID(account, rowASIN, position, "highlight", validAnnotation), asin: rowASIN},
		{name: "annotation が空", rawID: encodeRowID(account, rowASIN, position, "NOTE", ""), asin: rowASIN},
		{name: "annotation に _ を含む", rawID: encodeRowID(account, rowASIN, position, "NOTE", "a00000000_0000"), asin: rowASIN},
		{name: "annotation に非 ASCII", rawID: encodeRowID(account, rowASIN, position, "NOTE", "annotationあ"), asin: rowASIN},
		{name: "annotation に空白", rawID: encodeRowID(account, rowASIN, position, "NOTE", "annotation value"), asin: rowASIN},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewHighlightID(tt.rawID, tt.asin)
			if got != "" {
				t.Errorf("NewHighlightID(%q, %q) = %q, want %q", tt.rawID, tt.asin, got, "")
			}
			if !errors.Is(err, ErrMissingAnnotationID) {
				t.Errorf("errors.Is(err, ErrMissingAnnotationID) = %v, want %v", errors.Is(err, ErrMissingAnnotationID), true)
			}
			if err == nil {
				t.Fatal("NewHighlightID() error = nil, want error")
			}
			leaks := []string{tt.asin}
			if tt.rawID != "" {
				leaks = append(leaks, tt.rawID)
			}
			decoder := base64.RawStdEncoding
			if tt.standardEncoding {
				decoder = base64.StdEncoding
			}
			if decoded, err := decoder.DecodeString(tt.rawID); err == nil {
				leaks = append(leaks, strings.Split(string(decoded), ":")...)
			}
			for _, leak := range leaks {
				if leak == "" {
					continue
				}
				if got := strings.Contains(err.Error(), leak); got {
					t.Errorf("strings.Contains(err.Error(), %q) = %v, want %v", leak, got, false)
				}
			}
		})
	}
}

func encodeRowID(fields ...string) string {
	return base64.RawStdEncoding.EncodeToString([]byte(strings.Join(fields, ":")))
}
