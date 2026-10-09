// Package domain holds the canonical model shared by the Kindle source, the Notion destination and the state store.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const (
	fallbackIDPrefix = "b1-"
	idSchemeVersion  = "v1"
	idFieldSeparator = "\x1f"
	idHashPrefixLen  = 32
)

// Book は Canonical Model の書籍。
type Book struct {
	ID              string
	ASIN            string
	Title           string
	Author          string
	LastAnnotatedAt *time.Time
}

// NewBookID は ASIN があればそのまま、無ければ title と author から b1- プレフィックス付きの代替値を返す。
func NewBookID(asin, title, author string) string {
	if asin != "" {
		return asin
	}
	sum := sha256.Sum256([]byte(idSchemeVersion + idFieldSeparator + normalizeForID(title) + idFieldSeparator + normalizeForID(author)))
	return fallbackIDPrefix + hex.EncodeToString(sum[:])[:idHashPrefixLen]
}

// normalizeForID は §14 の正規化（NFC → 前後空白除去 → CRLF→LF → 水平空白の畳み込み）を行う。
func normalizeForID(s string) string {
	s = norm.NFC.String(s)
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if r == '\t' || unicode.Is(unicode.Zs, r) {
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
			continue
		}
		inSpace = false
		b.WriteRune(r)
	}
	return b.String()
}
