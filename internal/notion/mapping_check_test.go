package notion

import (
	"reflect"
	"testing"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
)

func TestCheckMapping(t *testing.T) {
	tests := []struct {
		name  string
		props []Property
		want  MappingCheck
	}{
		{
			name: "title 不足",
			props: []Property{
				{ID: "%3Axs", Name: "Kindle ID", Type: "rich_text"},
			},
			want: MappingCheck{
				Missing: []Problem{{Field: FieldTitle, Want: "title"}},
			},
		},
		{
			name: "kindle_id 不足",
			props: []Property{
				{ID: "title-id", Name: "Name", Type: "title"},
			},
			want: MappingCheck{
				Missing: []Problem{{Field: FieldKindleID, Want: "rich_text"}},
			},
		},
		{
			name:  "title と kindle_id が不足",
			props: []Property{},
			want: MappingCheck{
				Missing: []Problem{
					{Field: FieldTitle, Want: "title"},
					{Field: FieldKindleID, Want: "rich_text"},
				},
			},
		},
		{
			name: "kindle_id の型違い",
			props: []Property{
				{ID: "title-id", Name: "Name", Type: "title"},
				{ID: "kindle-number", Name: "Kindle ID", Type: "number"},
			},
			want: MappingCheck{
				Mismatched: []Problem{
					{Field: FieldKindleID, Property: &Property{ID: "kindle-number", Name: "Kindle ID", Type: "number"}, Want: "rich_text"},
				},
			},
		},
		{
			name: "型違いがあっても別の rich_text 候補がある",
			props: []Property{
				{ID: "title-id", Name: "Name", Type: "title"},
				{ID: "kindle-number", Name: "Kindle ID", Type: "number"},
				{ID: "book-id", Name: "Book ID", Type: "rich_text"},
			},
			want: MappingCheck{
				Mismatched: []Problem{
					{Field: FieldKindleID, Property: &Property{ID: "kindle-number", Name: "Kindle ID", Type: "number"}, Want: "rich_text"},
				},
			},
		},
		{
			name: "任意項目の型違い",
			props: []Property{
				{ID: "title-id", Name: "Name", Type: "title"},
				{ID: "%3Axs", Name: "Kindle ID", Type: "rich_text"},
				{ID: "author-number", Name: "著者", Type: "number"},
				{ID: "asin-select", Name: "ASIN", Type: "select"},
				{ID: "date-rich-text", Name: "Last Annotated", Type: "rich_text"},
			},
			want: MappingCheck{
				Skipped: []Problem{
					{Field: FieldAuthor, Property: &Property{ID: "author-number", Name: "著者", Type: "number"}, Want: "rich_text"},
					{Field: FieldASIN, Property: &Property{ID: "asin-select", Name: "ASIN", Type: "select"}, Want: "rich_text"},
					{Field: FieldLastAnnotatedAt, Property: &Property{ID: "date-rich-text", Name: "Last Annotated", Type: "rich_text"}, Want: "date"},
				},
			},
		},
		{
			name: "型の合う候補はあるが既定名一致が無い",
			props: []Property{
				{ID: "title-id", Name: "Name", Type: "title"},
				{ID: "book-id", Name: "Book ID", Type: "rich_text"},
			},
			want: MappingCheck{},
		},
		{
			name: "型が合う Data Source",
			props: []Property{
				{ID: "title-id", Name: "Title", Type: "title"},
				{ID: "%3Axs", Name: "Kindle ID", Type: "rich_text"},
				{ID: "Ab12", Name: "Author", Type: "rich_text"},
				{ID: "asin-id", Name: "ASIN", Type: "rich_text"},
				{ID: "date-id", Name: "Last Annotated At", Type: "date"},
			},
			want: MappingCheck{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CheckMapping(tt.props); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CheckMapping() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestMappingCheckErr(t *testing.T) {
	tests := []struct {
		name      string
		props     []Property
		wantError string
		wantCode  int
	}{
		{
			name: "title 不足と kindle_id の型違いが同時",
			props: []Property{
				{ID: "kindle-number", Name: "Kindle ID", Type: "number"},
			},
			wantError: "Notion で型 title の Property を足してください（名前は問いません）; Notion で Property「Kindle ID」の型を number から rich_text に直してください",
			wantCode:  3,
		},
		{
			name: "kindle_id 不足",
			props: []Property{
				{ID: "title-id", Name: "Name", Type: "title"},
			},
			wantError: "Notion で Property「Kindle ID」を型 rich_text で足してください",
			wantCode:  3,
		},
		{
			name: "kindle_id の候補が number",
			props: []Property{
				{ID: "title-id", Name: "Name", Type: "title"},
				{ID: "kindle-number", Name: "Kindle ID", Type: "number"},
			},
			wantError: "Notion で Property「Kindle ID」の型を number から rich_text に直してください",
			wantCode:  3,
		},
		{
			name: "title と kindle_id の両方が不足",
			props: []Property{
				{ID: "memo-id", Name: "Memo", Type: "number"},
			},
			wantError: "Notion で型 title の Property を足してください（名前は問いません）; Notion で Property「Kindle ID」を型 rich_text で足してください",
			wantCode:  3,
		},
		{
			name: "問題なし",
			props: []Property{
				{ID: "title-id", Name: "Name", Type: "title"},
				{ID: "%3Axs", Name: "Kindle ID", Type: "rich_text"},
			},
		},
		{
			name: "Skipped のみ",
			props: []Property{
				{ID: "title-id", Name: "Name", Type: "title"},
				{ID: "%3Axs", Name: "Kindle ID", Type: "rich_text"},
				{ID: "author-number", Name: "Author", Type: "number"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckMapping(tt.props).Err()
			if tt.wantError == "" {
				if err != nil {
					t.Fatalf("Err() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Err() = nil, want error")
			}
			if got := err.Error(); got != tt.wantError {
				t.Errorf("Err().Error() = %q, want %q", got, tt.wantError)
			}
			if got := exitcode.Code(err); got != tt.wantCode {
				t.Errorf("exitcode.Code() = %d, want %d", got, tt.wantCode)
			}
		})
	}
}

func TestMappingCheckSkippedLines(t *testing.T) {
	tests := []struct {
		name  string
		props []Property
		want  []string
	}{
		{
			name: "複数の型違い",
			props: []Property{
				{ID: "title-id", Name: "Name", Type: "title"},
				{ID: "%3Axs", Name: "Kindle ID", Type: "rich_text"},
				{ID: "author-number", Name: "Author", Type: "number"},
				{ID: "asin-select", Name: "ASIN", Type: "select"},
				{ID: "date-rich-text", Name: "Last Annotated", Type: "rich_text"},
			},
			want: []string{
				`not mapping property "Author" to author: type is number, want rich_text`,
				`not mapping property "ASIN" to asin: type is select, want rich_text`,
				`not mapping property "Last Annotated" to last_annotated_at: type is rich_text, want date`,
			},
		},
		{
			name:  "型違い無し",
			props: []Property{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CheckMapping(tt.props).SkippedLines(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SkippedLines() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
