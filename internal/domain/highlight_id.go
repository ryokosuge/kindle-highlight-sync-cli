package domain

import "strings"

// HighlightIDPrefix は採用した Highlight ID 方式（§16.1 Annotation ID Scheme）の prefix。
// 方式は 1 リリースにつき 1 種類だけなので（§16）、設定や引数で切り替えない。
const HighlightIDPrefix = "a-"

// IsAdoptedHighlightID は id が採用した方式（HighlightIDPrefix）の Highlight ID かを返す。
func IsAdoptedHighlightID(id string) bool {
	return HighlightIDPrefixOf(id) == HighlightIDPrefix
}

// HighlightIDPrefixOf は id の先頭から最初の `-` までを返す。`-` が無ければ空文字を返す。
func HighlightIDPrefixOf(id string) string {
	i := strings.IndexByte(id, '-')
	if i < 0 {
		return ""
	}
	return id[:i+1]
}
