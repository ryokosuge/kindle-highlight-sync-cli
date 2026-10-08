package notion

import (
	"strings"
	"unicode"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
)

// Field は Data Source の Property を対応させる項目（仕様書 §45）。
type Field string

// Field の値は config.toml の [notion.mapping] の key と同じ。
const (
	// FieldTitle はタイトル項目を表す。
	FieldTitle Field = "title"
	// FieldKindleID は Kindle ID 項目を表す。
	FieldKindleID Field = "kindle_id"
	// FieldAuthor は著者項目を表す。
	FieldAuthor Field = "author"
	// FieldASIN は ASIN 項目を表す。
	FieldASIN Field = "asin"
	// FieldLastAnnotatedAt は最終注釈日時項目を表す。
	FieldLastAnnotatedAt Field = "last_annotated_at"
)

const (
	typeTitle    = "title"
	typeRichText = "rich_text"
	typeDate     = "date"
)

// fields は §45 の順。
var fields = []Field{
	FieldTitle,
	FieldKindleID,
	FieldAuthor,
	FieldASIN,
	FieldLastAnnotatedAt,
}

// fieldTypes は §45 の型。
var fieldTypes = map[Field]string{
	FieldTitle:           typeTitle,
	FieldKindleID:        typeRichText,
	FieldAuthor:          typeRichText,
	FieldASIN:            typeRichText,
	FieldLastAnnotatedAt: typeDate,
}

// defaultNames は既定名。title は名前ではなく型で決めるので持たない。
var defaultNames = map[Field][]string{
	FieldKindleID:        {"Kindle ID"},
	FieldAuthor:          {"Author", "著者"},
	FieldASIN:            {"ASIN"},
	FieldLastAnnotatedAt: {"Last Annotated At", "Last Annotated"},
}

// Required は title と kindle_id で true（§45）。
func (f Field) Required() bool {
	return f == FieldTitle || f == FieldKindleID
}

// normalizeName は大文字小文字・空白・'_'・'-' を無視して名前を比べるための形にする。
func normalizeName(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '_' || r == '-' {
			return -1
		}
		return r
	}, strings.ToLower(name))
}

func matchesDefaultName(f Field, name string) bool {
	normalized := normalizeName(name)
	for _, defaultName := range defaultNames[f] {
		if normalizeName(defaultName) == normalized {
			return true
		}
	}
	return false
}

// Candidates は Field ごとに §45 の型が合う Property を props の順で返す。候補の無い Field は含まない。
func Candidates(props []Property) map[Field][]Property {
	candidates := make(map[Field][]Property)
	for _, f := range fields {
		for _, prop := range props {
			if prop.Type == fieldTypes[f] {
				candidates[f] = append(candidates[f], prop)
			}
		}
	}
	return candidates
}

// DefaultMapping は既定の対応（Property ID）と、既定が決まらなかった必須 Field（§45 の順）を返す。
// title は title 型の Property がちょうど 1 つならそれ。他は型が合う候補のうち既定名に一致するものがちょうど 1 つならそれ。
func DefaultMapping(props []Property) (config.Mapping, []Field) {
	candidates := Candidates(props)
	var mapping config.Mapping
	var missing []Field

	for _, f := range fields {
		var id string
		if f == FieldTitle {
			if len(candidates[f]) == 1 {
				id = candidates[f][0].ID
			}
		} else {
			var matches []Property
			for _, prop := range candidates[f] {
				if matchesDefaultName(f, prop.Name) {
					matches = append(matches, prop)
				}
			}
			if len(matches) == 1 {
				id = matches[0].ID
			}
		}

		if id == "" && f.Required() {
			missing = append(missing, f)
		}
		setMappingField(&mapping, f, id)
	}

	return mapping, missing
}

func setMappingField(mapping *config.Mapping, field Field, id string) {
	switch field {
	case FieldTitle:
		mapping.Title = id
	case FieldKindleID:
		mapping.KindleID = id
	case FieldAuthor:
		mapping.Author = id
	case FieldASIN:
		mapping.ASIN = id
	case FieldLastAnnotatedAt:
		mapping.LastAnnotatedAt = id
	}
}
