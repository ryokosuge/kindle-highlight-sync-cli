package destination

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
)

const lastAnnotatedAtPropertyID = "lat%3D"

var (
	policyMapping = config.Mapping{
		Title:           titlePropertyID,
		KindleID:        kindleIDPropertyID,
		Author:          authorPropertyID,
		ASIN:            asinPropertyID,
		LastAnnotatedAt: lastAnnotatedAtPropertyID,
	}
	policyTime = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
)

func policyBook(asin, author string, lastAnnotatedAt *time.Time) domain.Book {
	book := kindleBook(asin, "Kindle title", author)
	book.LastAnnotatedAt = lastAnnotatedAt
	return book
}

func policyPage(asin, author string) IndexedBook {
	return page("page-1", "", asin, "Notion title", author)
}

func TestPlanBookProperties(t *testing.T) {
	date := policyTime
	tests := []struct {
		name    string
		current IndexedBook
		book    domain.Book
		mapping config.Mapping
		want    BookPropertyValues
	}{
		// author の段
		{
			name:    "今が空で Kindle が非空なら書く",
			current: policyPage(testASIN, ""),
			book:    policyBook(testASIN, "Kindle Author", nil),
			mapping: policyMapping,
			want: BookPropertyValues{
				Author: &TextPropertyValue{PropertyID: authorPropertyID, Value: "Kindle Author"},
			},
		},
		{
			name:    "今が空白だけで Kindle が非空なら書く",
			current: policyPage(testASIN, " \u3000\t"),
			book:    policyBook(testASIN, "Kindle Author", nil),
			mapping: policyMapping,
			want: BookPropertyValues{
				Author: &TextPropertyValue{PropertyID: authorPropertyID, Value: "Kindle Author"},
			},
		},
		{
			name:    "今が非空なら書かない",
			current: policyPage(testASIN, "Kindle Author"),
			book:    policyBook(testASIN, "Kindle Author", nil),
			mapping: policyMapping,
			want:    BookPropertyValues{},
		},
		{
			name:    "Kindle が空なら書かない",
			current: policyPage(testASIN, ""),
			book:    policyBook(testASIN, "", nil),
			mapping: policyMapping,
			want:    BookPropertyValues{},
		},
		{
			name:    "Kindle が空白だけなら書かない",
			current: policyPage(testASIN, ""),
			book:    policyBook(testASIN, " \u3000\t", nil),
			mapping: policyMapping,
			want:    BookPropertyValues{},
		},
		{
			name:    "今も Kindle も非空で値が違っても書かない",
			current: policyPage(testASIN, "Existing Author"),
			book:    policyBook(testASIN, "Kindle Author", nil),
			mapping: policyMapping,
			want:    BookPropertyValues{},
		},
		// asin の段
		{
			name:    "今が空で Kindle が非空なら書く",
			current: policyPage("", "Existing Author"),
			book:    policyBook(testASIN, "Kindle Author", nil),
			mapping: policyMapping,
			want: BookPropertyValues{
				ASIN: &TextPropertyValue{PropertyID: asinPropertyID, Value: testASIN},
			},
		},
		{
			name:    "今が空白だけで Kindle が非空なら書く",
			current: policyPage(" \u3000\t", "Existing Author"),
			book:    policyBook(testASIN, "Kindle Author", nil),
			mapping: policyMapping,
			want: BookPropertyValues{
				ASIN: &TextPropertyValue{PropertyID: asinPropertyID, Value: testASIN},
			},
		},
		{
			name:    "今が非空なら書かない",
			current: policyPage(testOtherASIN, "Existing Author"),
			book:    policyBook(testASIN, "Kindle Author", nil),
			mapping: policyMapping,
			want:    BookPropertyValues{},
		},
		{
			name:    "Kindle が空なら書かない",
			current: policyPage("", "Existing Author"),
			book:    policyBook("", "Kindle Author", nil),
			mapping: policyMapping,
			want:    BookPropertyValues{},
		},
		{
			name:    "Kindle が空白だけなら書かない",
			current: policyPage("", "Existing Author"),
			book:    policyBook(" \u3000\t", "Kindle Author", nil),
			mapping: policyMapping,
			want:    BookPropertyValues{},
		},
		// last_annotated_at の段
		{
			name:    "非 nil なら今の値と比べずに書く",
			current: policyPage("", ""),
			book:    policyBook("", "", &date),
			mapping: policyMapping,
			want: BookPropertyValues{
				LastAnnotatedAt: &DatePropertyValue{PropertyID: lastAnnotatedAtPropertyID, Value: policyTime},
			},
		},
		{
			name:    "nil なら書かない",
			current: policyPage(testASIN, "Kindle Author"),
			book:    policyBook(testASIN, "Kindle Author", nil),
			mapping: policyMapping,
			want:    BookPropertyValues{},
		},
		{
			name:    "author と asin を書かなくても last_annotated_at は書く",
			current: policyPage(testASIN, "Kindle Author"),
			book:    policyBook(testASIN, "Kindle Author", &date),
			mapping: policyMapping,
			want: BookPropertyValues{
				LastAnnotatedAt: &DatePropertyValue{PropertyID: lastAnnotatedAtPropertyID, Value: policyTime},
			},
		},
		// mapping の段
		{
			name:    "Author ID が空なら該当しても author を書かない",
			current: policyPage(testASIN, ""),
			book:    policyBook(testASIN, "Kindle Author", nil),
			mapping: config.Mapping{ASIN: asinPropertyID, LastAnnotatedAt: lastAnnotatedAtPropertyID},
			want:    BookPropertyValues{},
		},
		{
			name:    "ASIN ID が空なら該当しても asin を書かない",
			current: policyPage("", "Existing Author"),
			book:    policyBook(testASIN, "Kindle Author", nil),
			mapping: config.Mapping{Author: authorPropertyID, LastAnnotatedAt: lastAnnotatedAtPropertyID},
			want:    BookPropertyValues{},
		},
		{
			name:    "LastAnnotatedAt ID が空なら非 nil でも書かない",
			current: policyPage(testASIN, "Kindle Author"),
			book:    policyBook(testASIN, "Kindle Author", &date),
			mapping: config.Mapping{Author: authorPropertyID, ASIN: asinPropertyID},
			want:    BookPropertyValues{},
		},
		{
			name:    "すべての mapping が空なら該当しても何も書かない",
			current: policyPage("", ""),
			book:    policyBook(testASIN, "Kindle Author", &date),
			mapping: config.Mapping{},
			want:    BookPropertyValues{},
		},
		{
			name:    "3 つすべてを書ける",
			current: policyPage("", ""),
			book:    policyBook(testASIN, "Kindle Author", &date),
			mapping: policyMapping,
			want: BookPropertyValues{
				Author:          &TextPropertyValue{PropertyID: authorPropertyID, Value: "Kindle Author"},
				ASIN:            &TextPropertyValue{PropertyID: asinPropertyID, Value: testASIN},
				LastAnnotatedAt: &DatePropertyValue{PropertyID: lastAnnotatedAtPropertyID, Value: policyTime},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PlanBookProperties(tt.current, tt.book, tt.mapping)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("PlanBookProperties() = %s, want %s", formatPropertyValues(got), formatPropertyValues(tt.want))
			}
		})
	}
}

func TestPlanBookPropertiesCopiesLastAnnotatedAt(t *testing.T) {
	sourceTime := policyTime
	got := PlanBookProperties(
		policyPage(testASIN, "Kindle Author"),
		policyBook(testASIN, "Kindle Author", &sourceTime),
		policyMapping,
	)
	sourceTime = sourceTime.Add(time.Hour)
	if got.LastAnnotatedAt.Value != policyTime {
		t.Errorf("LastAnnotatedAt.Value = %s, want %s", got.LastAnnotatedAt.Value.Format(time.RFC3339Nano), policyTime.Format(time.RFC3339Nano))
	}
}

func TestBookPropertyValuesIsEmpty(t *testing.T) {
	tests := []struct {
		name  string
		value BookPropertyValues
		want  bool
	}{
		{name: "ゼロ値", value: BookPropertyValues{}, want: true},
		{name: "Author だけ", value: BookPropertyValues{Author: &TextPropertyValue{}}, want: false},
		{name: "ASIN だけ", value: BookPropertyValues{ASIN: &TextPropertyValue{}}, want: false},
		{name: "LastAnnotatedAt だけ", value: BookPropertyValues{LastAnnotatedAt: &DatePropertyValue{}}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.value.IsEmpty(); got != tt.want {
				t.Errorf("IsEmpty() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBookPropertyValuesFields(t *testing.T) {
	typeOfValues := reflect.TypeOf(BookPropertyValues{})
	fields := make([]string, typeOfValues.NumField())
	for i := range fields {
		fields[i] = typeOfValues.Field(i).Name
	}
	want := []string{"Author", "ASIN", "LastAnnotatedAt"}
	if !reflect.DeepEqual(fields, want) {
		t.Errorf("BookPropertyValues fields = %v, want %v", fields, want)
	}
}

func formatPropertyValues(v BookPropertyValues) string {
	formatText := func(value *TextPropertyValue) string {
		if value == nil {
			return "<nil>"
		}
		return fmt.Sprintf("{PropertyID:%q, Value:%q}", value.PropertyID, value.Value)
	}
	formatDate := func(value *DatePropertyValue) string {
		if value == nil {
			return "<nil>"
		}
		return fmt.Sprintf("{PropertyID:%q, Value:%s}", value.PropertyID, value.Value.Format(time.RFC3339Nano))
	}
	return fmt.Sprintf(
		"{Author:%s, ASIN:%s, LastAnnotatedAt:%s}",
		formatText(v.Author),
		formatText(v.ASIN),
		formatDate(v.LastAnnotatedAt),
	)
}
