package domain

import "testing"

func TestNormalizeForMatch(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "全角英数字は半角になる",
			in:   "ＧＯ１２３",
			want: "go123",
		},
		{
			name: "半角英数字はそのまま小文字になる",
			in:   "Go123",
			want: "go123",
		},
		{
			name: "大文字は小文字になる",
			in:   "THE GO PROGRAMMING LANGUAGE",
			want: "thegoprogramminglanguage",
		},
		{
			name: "ギリシャ文字の語末の大文字シグマは小文字の語末形と同じ値になる",
			in:   "ΟΣ",
			want: "ος",
		},
		{
			name: "ギリシャ文字の語末形の小文字シグマはそのまま",
			in:   "ος",
			want: "ος",
		},
		{
			name: "ギリシャ文字の大文字だけの単語は先頭だけ大文字の単語と同じ値になる",
			in:   "ΟΔΥΣΣΕΥΣ",
			want: "οδυσσευς",
		},
		{
			name: "ギリシャ文字の先頭だけ大文字の単語は小文字になる",
			in:   "Οδυσσευς",
			want: "οδυσσευς",
		},
		{
			name: "前後の半角 space を除く",
			in:   "  Go  ",
			want: "go",
		},
		{
			name: "途中の半角 space を除く",
			in:   "The Go Programming",
			want: "thegoprogramming",
		},
		{
			name: "全角空白を除く",
			in:   "\u3000吾輩は\u3000猫である\u3000",
			want: "吾輩は猫である",
		},
		{
			name: "tab を除く",
			in:   "\tGo\tProgramming\t",
			want: "goprogramming",
		},
		{
			name: "改行（LF / CRLF / CR）を除く",
			in:   "\nGo\r\nProgramming\rLanguage\n",
			want: "goprogramminglanguage",
		},
		{
			name: "NBSP を除く",
			in:   "Go\u00a0Programming",
			want: "goprogramming",
		},
		{
			name: "NFC の濁点付き文字",
			in:   "\u30ac\u30a4\u30c9",
			want: "\u30ac\u30a4\u30c9",
		},
		{
			name: "NFD の濁点付き文字は NFC と同じ値になる",
			in:   "\u30ab\u3099\u30a4\u30c8\u3099",
			want: "\u30ac\u30a4\u30c9",
		},
		{
			name: "NFC のアクセント付き文字",
			in:   "Caf\u00e9",
			want: "caf\u00e9",
		},
		{
			name: "NFD のアクセント付き文字は NFC と同じ値になる",
			in:   "Cafe\u0301",
			want: "caf\u00e9",
		},
		{
			name: "空文字は空文字",
			in:   "",
			want: "",
		},
		{
			name: "空白だけの文字列は空文字になる",
			in:   " \t\u3000\r\n\u00a0 ",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeForMatch(tt.in)
			if got != tt.want {
				t.Errorf("NormalizeForMatch(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
