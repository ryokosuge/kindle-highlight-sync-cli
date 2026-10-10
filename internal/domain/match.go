package domain

import (
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

// NormalizeForMatch は Title / Author の照合用の正規化（§15）を NFKC → 全空白の除去 → 小文字化 → NFC の順で行う。
// 小文字化は前後の文字で変わる（ギリシャ文字の語末シグマ）ので空白を除いてからかけ、小文字化で残る分解形は最後の NFC で合成する。
// 照合にだけ使い、Book ID の生成（normalizeForID）には使わない。
func NormalizeForMatch(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, norm.NFKC.String(s))
	return norm.NFC.String(cases.Lower(language.Und).String(s))
}
