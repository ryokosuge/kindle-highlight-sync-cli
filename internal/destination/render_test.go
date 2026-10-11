package destination

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/notion"
)

func TestRenderHighlight(t *testing.T) {
	tests := []struct {
		name string
		h    domain.Highlight
		want notion.QuoteBlock
	}{
		// 通常
		{
			name: "Note の付いた Highlight は本文・改行・Location · Page · Sync Marker と子の paragraph になる",
			h:    domain.Highlight{ID: "a-highlight-abc", Text: "本文", Note: "メモ", Location: "123", Page: "45"},
			want: notion.QuoteBlock{
				RichText: []notion.RichText{{Content: "本文"}, {Content: "\nLocation 123 · Page 45 · khs:a-highlight-abc"}},
				Children: []notion.ParagraphBlock{{RichText: []notion.RichText{{Content: "メモ"}}}},
			},
		},
		{
			name: "Note の無い Highlight は子を持たない",
			h:    domain.Highlight{ID: "a-highlight-abc", Text: "本文", Location: "123"},
			want: notion.QuoteBlock{
				RichText: []notion.RichText{{Content: "本文"}, {Content: "\nLocation 123 · khs:a-highlight-abc"}},
			},
		},
		{
			name: "Page の無い Highlight は Location と Sync Marker だけになる",
			h:    domain.Highlight{ID: "a-highlight-abc", Text: "本文", Note: "メモ", Location: "123"},
			want: notion.QuoteBlock{
				RichText: []notion.RichText{{Content: "本文"}, {Content: "\nLocation 123 · khs:a-highlight-abc"}},
				Children: []notion.ParagraphBlock{{RichText: []notion.RichText{{Content: "メモ"}}}},
			},
		},
		{
			name: "Location の無い Highlight は Page と Sync Marker だけになる",
			h:    domain.Highlight{ID: "a-highlight-abc", Text: "本文", Page: "45"},
			want: notion.QuoteBlock{
				RichText: []notion.RichText{{Content: "本文"}, {Content: "\nPage 45 · khs:a-highlight-abc"}},
			},
		},
		{
			name: "Location も Page も無ければ最後の行は Sync Marker だけになる",
			h:    domain.Highlight{ID: "a-highlight-abc", Text: "本文"},
			want: notion.QuoteBlock{
				RichText: []notion.RichText{{Content: "本文"}, {Content: "\nkhs:a-highlight-abc"}},
			},
		},
		// Note-only（§19.1）
		{
			name: "a-note- の Highlight は Note が本文になり子を持たない",
			h:    domain.Highlight{ID: "a-note-abc", Note: "メモ", Location: "123"},
			want: notion.QuoteBlock{
				RichText: []notion.RichText{{Content: "メモ"}, {Content: "\nLocation 123 · khs:a-note-abc"}},
			},
		},
		{
			name: "a-note- の Highlight は Text があっても Note を本文にする",
			h:    domain.Highlight{ID: "a-note-abc", Text: "本文", Note: "メモ"},
			want: notion.QuoteBlock{
				RichText: []notion.RichText{{Content: "メモ"}, {Content: "\nkhs:a-note-abc"}},
			},
		},
		// 本文非表示（§19.2）
		{
			name: "a-highlight- で本文が空なら (Highlight text unavailable) になり Note は子の paragraph になる",
			h:    domain.Highlight{ID: "a-highlight-abc", Note: "メモ", Location: "123"},
			want: notion.QuoteBlock{
				RichText: []notion.RichText{{Content: "(Highlight text unavailable)"}, {Content: "\nLocation 123 · khs:a-highlight-abc"}},
				Children: []notion.ParagraphBlock{{RichText: []notion.RichText{{Content: "メモ"}}}},
			},
		},
		{
			name: "a-highlight- で本文も Note も空なら本文だけ (Highlight text unavailable) になる",
			h:    domain.Highlight{ID: "a-highlight-abc"},
			want: notion.QuoteBlock{
				RichText: []notion.RichText{{Content: "(Highlight text unavailable)"}, {Content: "\nkhs:a-highlight-abc"}},
			},
		},
		// 本文の中の改行
		{
			name: "本文の改行はそのまま残す",
			h:    domain.Highlight{ID: "a-highlight-abc", Text: "1 行目\n2 行目", Location: "7"},
			want: notion.QuoteBlock{
				RichText: []notion.RichText{{Content: "1 行目\n2 行目"}, {Content: "\nLocation 7 · khs:a-highlight-abc"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderHighlight(tt.h)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("renderHighlight(%+v) = %+v, want %+v", tt.h, got, tt.want)
			}
		})
	}
}

// joinRichText は Notion が plain_text を連結するのと同じく、rich_text の content を順に連結する。
func joinRichText(items []notion.RichText) string {
	var b strings.Builder
	for _, item := range items {
		b.WriteString(item.Content)
	}
	return b.String()
}

func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}

// assertSplitRichText は items が UTF-16 で limit 以下・サロゲートペアを割らない・連結すると want に戻ることを確かめる。
func assertSplitRichText(t *testing.T, items []notion.RichText, want string) {
	t.Helper()
	for i, item := range items {
		if n := utf16Len(item.Content); n > richTextContentLimit {
			t.Errorf("rich_text[%d] の UTF-16 長 = %d, want <= %d", i, n, richTextContentLimit)
		}
		if !utf8.ValidString(item.Content) {
			t.Errorf("rich_text[%d] がサロゲートペアの途中で切れている", i)
		}
	}
	if got := joinRichText(items); got != want {
		t.Errorf("rich_text の連結の長さ = %d, want %d（元の本文に戻らない）", len(got), len(want))
	}
}

func TestRenderHighlightLongText(t *testing.T) {
	const marker = "\nLocation 12 · khs:a-highlight-long"
	tests := []struct {
		name          string
		text          string
		wantRichTexts int
	}{
		{name: "日本語 4,500 文字は 3 要素に分かれる", text: strings.Repeat("あ", 4500), wantRichTexts: 3},
		{name: "絵文字 2,000 個（UTF-16 で 4,000）は 2 要素に分かれる", text: strings.Repeat("😀", 2000), wantRichTexts: 2},
		{name: "日本語と絵文字の混在は境目でサロゲートペアを割らない", text: strings.Repeat("あ😀", 1500), wantRichTexts: 3},
		{name: "ちょうど 2,000 文字は 1 要素のまま", text: strings.Repeat("あ", 2000), wantRichTexts: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderHighlight(domain.Highlight{ID: "a-highlight-long", Text: tt.text, Location: "12"})
			if len(got.Children) != 0 {
				t.Errorf("Children = %d, want 0", len(got.Children))
			}
			n := len(got.RichText)
			if n != tt.wantRichTexts+1 {
				t.Fatalf("rich_text の要素数 = %d, want %d（本文 %d + 最後の行 1）", n, tt.wantRichTexts+1, tt.wantRichTexts)
			}
			if last := got.RichText[n-1].Content; last != marker {
				t.Errorf("最後の rich_text = %q, want %q", last, marker)
			}
			assertSplitRichText(t, got.RichText[:n-1], tt.text)
		})
	}
}

func TestRenderHighlightLongNote(t *testing.T) {
	note := strings.Repeat("😀あ", 1500)
	got := renderHighlight(domain.Highlight{ID: "a-highlight-abc", Text: "本文", Note: note})
	if want := []notion.RichText{{Content: "本文"}, {Content: "\nkhs:a-highlight-abc"}}; !reflect.DeepEqual(got.RichText, want) {
		t.Errorf("RichText = %+v, want %+v", got.RichText, want)
	}
	if len(got.Children) != 1 {
		t.Fatalf("Children = %d, want 1", len(got.Children))
	}
	items := got.Children[0].RichText
	if len(items) != 3 {
		t.Errorf("子の paragraph の rich_text の要素数 = %d, want 3", len(items))
	}
	assertSplitRichText(t, items, note)
}

func TestRenderHighlightSyncMarker(t *testing.T) {
	tests := []struct {
		name string
		h    domain.Highlight
	}{
		{name: "通常", h: domain.Highlight{ID: "a-highlight-abc", Text: "本文", Note: "メモ", Location: "123", Page: "45"}},
		{name: "本文に khs: がある", h: domain.Highlight{ID: "a-highlight-real", Text: "本文に khs:a-fake を書いた", Location: "1"}},
		{name: "本文の末尾が khs: で終わる", h: domain.Highlight{ID: "a-highlight-real", Text: "末尾 khs:a-fake"}},
		{name: "Note-only の Note に khs: がある", h: domain.Highlight{ID: "a-note-real", Note: "khs:a-fake"}},
		{name: "長い本文", h: domain.Highlight{ID: "a-highlight-long", Text: strings.Repeat("khs:a-fake ", 500), Location: "9"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderHighlight(tt.h)
			last := got.RichText[len(got.RichText)-1].Content
			if !strings.HasSuffix(last, " · khs:"+tt.h.ID) && !strings.HasSuffix(last, "\nkhs:"+tt.h.ID) {
				t.Errorf("最後の rich_text = %q, want Sync Marker khs:%s で終わる", last, tt.h.ID)
			}
			id, ok := SyncMarkerID(joinRichText(got.RichText))
			if !ok || id != tt.h.ID {
				t.Errorf("SyncMarkerID(連結) = (%q, %t), want (%q, true)", id, ok, tt.h.ID)
			}
		})
	}
}

func TestSplitText(t *testing.T) {
	tests := []struct {
		name string
		s    string
		max  int
		want []string
	}{
		{name: "空なら要素を作らない", s: "", max: 5, want: nil},
		{name: "max 以下はそのまま", s: "abcde", max: 5, want: []string{"abcde"}},
		{name: "ASCII を max ごとに分ける", s: "abcdefg", max: 3, want: []string{"abc", "def", "g"}},
		{name: "日本語は 1 文字 1 unit で数える", s: "あいうえお", max: 2, want: []string{"あい", "うえ", "お"}},
		{name: "サロゲートペアは 2 unit で数える", s: "😀😀😀", max: 4, want: []string{"😀😀", "😀"}},
		{name: "サロゲートペアの途中では切らない", s: "a😀b", max: 2, want: []string{"a", "😀", "b"}},
		{name: "境目がサロゲートペアに掛かれば手前で切る", s: "ab😀", max: 3, want: []string{"ab", "😀"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitText(tt.s, tt.max)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitText(%q, %d) = %q, want %q", tt.s, tt.max, got, tt.want)
			}
		})
	}
}
