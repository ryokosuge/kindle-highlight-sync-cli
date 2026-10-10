package domain

import "testing"

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
