package domain

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// NormalizeForMatch は Title / Author の照合用の正規化（§15: NFKC → 小文字化 → 全空白の除去）を行う。
// 照合にだけ使い、Book ID の生成（normalizeForID）には使わない。
func NormalizeForMatch(s string) string {
	s = strings.ToLower(norm.NFKC.String(s))
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}
