package destination

import (
	"strings"
	"time"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
)

// TextPropertyValue は書く rich_text Property の Property ID と値。
type TextPropertyValue struct {
	PropertyID string
	Value      string
}

// DatePropertyValue は書く date Property の Property ID と値。
type DatePropertyValue struct {
	PropertyID string
	Value      time.Time
}

// BookPropertyValues は Adopt と 2 回目以降の sync で書く Property（§12）。nil のものは書かない。
// title と Kindle ID Property は持たない（title は変えず、Kindle ID Property は Adopt の側で書く）。
type BookPropertyValues struct {
	Author          *TextPropertyValue
	ASIN            *TextPropertyValue
	LastAnnotatedAt *DatePropertyValue
}

// IsEmpty は書く Property が 1 つも無いかを返す。
func (v BookPropertyValues) IsEmpty() bool {
	return v.Author == nil && v.ASIN == nil && v.LastAnnotatedAt == nil
}

// PlanBookProperties は §12 に従い、author / asin は今の値が空のときだけ Kindle の値で埋め、Kindle の値が空なら書かない。
// last_annotated_at は Kindle の値があれば今の値と比べずに書き、mapping されていない Property は書かない。
func PlanBookProperties(current IndexedBook, book domain.Book, mapping config.Mapping) BookPropertyValues {
	values := BookPropertyValues{
		Author: fillIfBlank(mapping.Author, current.Author, book.Author),
		ASIN:   fillIfBlank(mapping.ASIN, current.ASIN, book.ASIN),
	}
	if mapping.LastAnnotatedAt != "" && book.LastAnnotatedAt != nil {
		values.LastAnnotatedAt = &DatePropertyValue{
			PropertyID: mapping.LastAnnotatedAt,
			Value:      *book.LastAnnotatedAt,
		}
	}
	return values
}

func fillIfBlank(propertyID, current, kindle string) *TextPropertyValue {
	if propertyID == "" || strings.TrimSpace(current) != "" || strings.TrimSpace(kindle) == "" {
		return nil
	}
	return &TextPropertyValue{PropertyID: propertyID, Value: kindle}
}
