package domain

import (
	"regexp"
	"testing"
)

var fallbackIDPattern = regexp.MustCompile(`^b1-[0-9a-f]{32}$`)

func TestNewBookID(t *testing.T) {
	tests := []struct {
		name   string
		asin   string
		title  string
		author string
		want   string
	}{
		{
			name:   "ASIN がある場合はそのまま返す",
			asin:   "B000123456",
			title:  "Title",
			author: "Author",
			want:   "B000123456",
		},
		{
			name: "ASIN があれば title と author が空でもそのまま返す",
			asin: "B000123456",
			want: "B000123456",
		},
		{
			name:   "ASIN が無い場合は b1- の代替値",
			title:  "Title",
			author: "Author",
			want:   "b1-6894016e730a078d3327173b78bf316f",
		},
		{
			name:   "日本語の title と author",
			title:  "吾輩は猫である",
			author: "夏目漱石",
			want:   "b1-2e2a93c68b85f3987e02e82f1ac956c7",
		},
		{
			name:  "author が空",
			title: "Title",
			want:  "b1-0810c538d4938cb42a0c0a73614e9dc8",
		},
		{
			name:   "NFC のカタカナ",
			title:  "ガイド",
			author: "Author",
			want:   "b1-c5e0724639fa3e8037570c75e08e2c6d",
		},
		{
			name:   "NFD は NFC と同じ ID になる",
			title:  "\u30ab\u3099\u30a4\u30c8\u3099",
			author: "Author",
			want:   "b1-c5e0724639fa3e8037570c75e08e2c6d",
		},
		{
			name:   "前後の空白は除去される",
			title:  "  Title \t",
			author: "Author",
			want:   "b1-6894016e730a078d3327173b78bf316f",
		},
		{
			name:   "全角空白と改行も前後なら除去される",
			title:  "\u3000Title\n",
			author: "Author",
			want:   "b1-6894016e730a078d3327173b78bf316f",
		},
		{
			name:   "author の前後の空白も除去される",
			title:  "Title",
			author: " Author ",
			want:   "b1-6894016e730a078d3327173b78bf316f",
		},
		{
			name:   "水平空白は畳まれる",
			title:  "a b",
			author: "Author",
			want:   "b1-98bd0bed963cb72360cc1fd2248954da",
		},
		{
			name:   "連続する空白は畳まれる",
			title:  "a  b",
			author: "Author",
			want:   "b1-98bd0bed963cb72360cc1fd2248954da",
		},
		{
			name:   "タブは空白として畳まれる",
			title:  "a\tb",
			author: "Author",
			want:   "b1-98bd0bed963cb72360cc1fd2248954da",
		},
		{
			name:   "空白とタブの混在も畳まれる",
			title:  "a \t b",
			author: "Author",
			want:   "b1-98bd0bed963cb72360cc1fd2248954da",
		},
		{
			name:   "全角空白も畳まれる",
			title:  "a\u3000b",
			author: "Author",
			want:   "b1-98bd0bed963cb72360cc1fd2248954da",
		},
		{
			name:   "NBSP も畳まれる",
			title:  "a b",
			author: "Author",
			want:   "b1-98bd0bed963cb72360cc1fd2248954da",
		},
		{
			name:   "CRLF は LF になる",
			title:  "a\r\nb",
			author: "Author",
			want:   "b1-42cd76a08a86bed2e17e21aec049f5a9",
		},
		{
			name:   "LF は畳まれない",
			title:  "a\nb",
			author: "Author",
			want:   "b1-42cd76a08a86bed2e17e21aec049f5a9",
		},
		{
			name:   "全角文字は NFKC されない",
			title:  "\uff21\uff22\uff23",
			author: "Author",
			want:   "b1-50d4ccd1631accbb143425585e870eb8",
		},
		{
			name:   "ASCII 大文字は小文字化されない",
			title:  "ABC",
			author: "Author",
			want:   "b1-03565aae18d9bbcd0fe53bc27f914684",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewBookID(tt.asin, tt.title, tt.author)
			if got != tt.want {
				t.Errorf("NewBookID(%q, %q, %q) = %q, want %q", tt.asin, tt.title, tt.author, got, tt.want)
			}
		})
	}
}

func TestNewBookIDFormat(t *testing.T) {
	got := NewBookID("", "Title", "Author")
	if !fallbackIDPattern.MatchString(got) {
		t.Errorf("NewBookID fallback = %q, want match %q", got, fallbackIDPattern)
	}
}

func TestNewBookIDDifferentIDs(t *testing.T) {
	tests := []struct {
		name   string
		title  string
		author string
		notID  string
	}{
		{
			name:  "LF の連続は畳まれないので a\\nb とは違う",
			title: "a\n\nb",
			notID: "b1-42cd76a08a86bed2e17e21aec049f5a9",
		},
		{
			name:  "LF は水平空白ではないので a b とは違う",
			title: "a\nb",
			notID: "b1-98bd0bed963cb72360cc1fd2248954da",
		},
		{
			name:  "小文字化しないので abc は ABC と違う",
			title: "abc",
			notID: "b1-03565aae18d9bbcd0fe53bc27f914684",
		},
		{
			name:  "空白は除去されないので ab は a b と違う",
			title: "ab",
			notID: "b1-98bd0bed963cb72360cc1fd2248954da",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewBookID("", tt.title, tt.author)
			if got == tt.notID {
				t.Errorf("NewBookID(%q, %q, %q) = %q, want different from %q", "", tt.title, tt.author, got, tt.notID)
			}
		})
	}
}

func TestNormalizeForID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "NFD を NFC にする",
			in:   "\u30ab\u3099\u30a4\u30c8\u3099",
			want: "ガイド",
		},
		{
			name: "前後の空白を除去する",
			in:   "  Title \t",
			want: "Title",
		},
		{
			name: "CRLF を LF にする",
			in:   "a\r\nb",
			want: "a\nb",
		},
		{
			name: "水平空白の連続を 1 つの空白に畳む",
			in:   "a \t\u3000 b",
			want: "a b",
		},
		{
			name: "LF の連続はそのまま",
			in:   "a\n\nb",
			want: "a\n\nb",
		},
		{
			name: "単独の CR はそのまま",
			in:   "a\rb",
			want: "a\rb",
		},
		{
			name: "全角英字は変換しない",
			in:   "\uff21\uff22\uff23",
			want: "ＡＢＣ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeForID(tt.in)
			if got != tt.want {
				t.Errorf("normalizeForID(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
