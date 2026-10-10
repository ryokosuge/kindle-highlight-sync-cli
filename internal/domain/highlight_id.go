package domain

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// HighlightIDPrefix は採用した Highlight ID 方式（§16.1 Annotation ID Scheme）の prefix。
// 方式は 1 リリースにつき 1 種類だけなので（§16）、設定や引数で切り替えない。
const HighlightIDPrefix = "a-"

// ErrMissingAnnotationID は §16.1 / §55 の採用方式に必要な identifier が取れないときのエラー。
// SourceFormat（exit 7）への分類は Notebook を読む internal/source で行い、この package では種類を付けない。
var ErrMissingAnnotationID = errors.New("domain: annotation ID is not available")

// NewHighlightID は Notebook の行 id から §16.1 の Highlight ID を作る。
func NewHighlightID(rawID, asin string) (string, error) {
	if rawID == "" {
		return "", fmt.Errorf("%w: row id is empty", ErrMissingAnnotationID)
	}
	decoded, err := base64.RawStdEncoding.DecodeString(rawID)
	if err != nil {
		return "", fmt.Errorf("%w: row id is not base64", ErrMissingAnnotationID)
	}
	fields := strings.Split(string(decoded), ":")
	if len(fields) != 5 {
		return "", fmt.Errorf("%w: row id does not have 5 fields", ErrMissingAnnotationID)
	}
	if fields[1] != asin {
		return "", fmt.Errorf("%w: row id belongs to another book", ErrMissingAnnotationID)
	}
	var kind string
	switch fields[3] {
	case "HIGHLIGHT":
		kind = "highlight"
	case "NOTE":
		kind = "note"
	default:
		return "", fmt.Errorf("%w: row id has an unknown type", ErrMissingAnnotationID)
	}
	if !validAnnotationID(fields[4]) {
		return "", fmt.Errorf("%w: row id has an invalid annotation", ErrMissingAnnotationID)
	}
	return HighlightIDPrefix + kind + "-" + fields[4], nil
}

func validAnnotationID(id string) bool {
	if id == "" {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

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
