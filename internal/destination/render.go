package destination

import (
	"strings"
	"unicode/utf16"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/notion"
)

// richTextContentLimit は rich_text 1 要素の text.content の上限。公式 doc は 2,000 文字とだけ書き、
// 数え方（UTF-16 か文字か）を示さないので、どちらでも超えない UTF-16 code unit で数える。
const richTextContentLimit = 2000

// unavailableHighlightText は本文非表示 entry（§19.2）の本文の代わりに出す文言。
const unavailableHighlightText = "(Highlight text unavailable)"

// Highlight ID の種類の prefix（§16.1 / §19）。Note-only と本文非表示はどちらも Text が空なので、ID の prefix で見分ける。
const (
	noteOnlyIDPrefix        = domain.HighlightIDPrefix + "note-"
	hiddenHighlightIDPrefix = domain.HighlightIDPrefix + "highlight-"
)

const (
	syncMarkerPrefix  = "khs:"
	metadataSeparator = " · "
)

// renderHighlight は Highlight を §23 の Quote Block にする。
// 本文は splitText で分け、改行・Location / Page・Sync Marker は最後の rich_text の要素にまとめて置く
// （ListHighlightIDs は quote の rich_text の連結から最後の khs: を取るので、§21 / §22）。
// 空の本文・空の Note の要素は作らない。
func renderHighlight(h domain.Highlight) notion.QuoteBlock {
	noteOnly := strings.HasPrefix(h.ID, noteOnlyIDPrefix)
	body := h.Text
	if noteOnly {
		body = h.Note
	} else if body == "" && strings.HasPrefix(h.ID, hiddenHighlightIDPrefix) {
		body = unavailableHighlightText
	}

	quote := notion.QuoteBlock{RichText: richTexts(body)}
	last := metadataLine(h)
	if body != "" {
		last = "\n" + last
	}
	quote.RichText = append(quote.RichText, notion.RichText{Content: last})

	if !noteOnly && h.Note != "" {
		quote.Children = []notion.ParagraphBlock{{RichText: richTexts(h.Note)}}
	}
	return quote
}

// metadataLine は Location / Page / Sync Marker を " · " でつないだ最後の行を返す。無い項目は省く。
func metadataLine(h domain.Highlight) string {
	parts := make([]string, 0, 3)
	if h.Location != "" {
		parts = append(parts, "Location "+h.Location)
	}
	if h.Page != "" {
		parts = append(parts, "Page "+h.Page)
	}
	parts = append(parts, syncMarkerPrefix+h.ID)
	return strings.Join(parts, metadataSeparator)
}

func richTexts(s string) []notion.RichText {
	var items []notion.RichText
	for _, part := range splitText(s, richTextContentLimit) {
		items = append(items, notion.RichText{Content: part})
	}
	return items
}

// splitText は s を UTF-16 で max 以下の要素に分ける（サロゲートペアを割らない）。s が空なら nil を返す。
func splitText(s string, max int) []string {
	var parts []string
	start, units := 0, 0
	for i, r := range s {
		n := utf16.RuneLen(r)
		if units+n > max {
			parts = append(parts, s[start:i])
			start, units = i, 0
		}
		units += n
	}
	if start < len(s) {
		parts = append(parts, s[start:])
	}
	return parts
}
