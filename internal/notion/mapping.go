package notion

import (
	"strings"
	"unicode"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
)

type Field string

const (
	FieldTitle           Field = "title"
	FieldKindleID        Field = "kindle_id"
	FieldAuthor          Field = "author"
	FieldASIN            Field = "asin"
	FieldLastAnnotatedAt Field = "last_annotated_at"
)

const (
	typeTitle    = "title"
	typeRichText = "rich_text"
	typeDate     = "date"
)

var fields = []Field{
	FieldTitle,
	FieldKindleID,
	FieldAuthor,
	FieldASIN,
	FieldLastAnnotatedAt,
}

var fieldTypes = map[Field]string{
	FieldTitle:           typeTitle,
	FieldKindleID:        typeRichText,
	FieldAuthor:          typeRichText,
	FieldASIN:            typeRichText,
	FieldLastAnnotatedAt: typeDate,
}

var defaultNames = map[Field][]string{
	FieldKindleID:        {"Kindle ID"},
	FieldAuthor:          {"Author", "著者"},
	FieldASIN:            {"ASIN"},
	FieldLastAnnotatedAt: {"Last Annotated At", "Last Annotated"},
}

func (f Field) Required() bool {
	return f == FieldTitle || f == FieldKindleID
}

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
