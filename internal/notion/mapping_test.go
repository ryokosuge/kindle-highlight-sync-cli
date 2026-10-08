package notion

import (
	"reflect"
	"slices"
	"testing"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
)

func TestFieldRequired(t *testing.T) {
	tests := []struct {
		name     string
		field    Field
		required bool
	}{
		{name: "title", field: FieldTitle, required: true},
		{name: "kindle_id", field: FieldKindleID, required: true},
		{name: "author", field: FieldAuthor, required: false},
		{name: "asin", field: FieldASIN, required: false},
		{name: "last_annotated_at", field: FieldLastAnnotatedAt, required: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.field.Required(); got != tt.required {
				t.Errorf("Required() = %t, want %t", got, tt.required)
			}
		})
	}
}

func TestCandidates(t *testing.T) {
	tests := []struct {
		name  string
		props []Property
		want  map[Field][]Property
	}{
		{
			name: "型が合う",
			props: []Property{
				{ID: "title-id", Name: "Name", Type: typeTitle},
				{ID: "%3Axs", Name: "Kindle ID", Type: typeRichText},
				{ID: "Ab12", Name: "Author", Type: typeRichText},
				{ID: "asin-id", Name: "ASIN", Type: typeRichText},
				{ID: "date-id", Name: "Last Annotated At", Type: typeDate},
			},
			want: map[Field][]Property{
				FieldTitle: {{ID: "title-id", Name: "Name", Type: typeTitle}},
				FieldKindleID: {
					{ID: "%3Axs", Name: "Kindle ID", Type: typeRichText},
					{ID: "Ab12", Name: "Author", Type: typeRichText},
					{ID: "asin-id", Name: "ASIN", Type: typeRichText},
				},
				FieldAuthor: {
					{ID: "%3Axs", Name: "Kindle ID", Type: typeRichText},
					{ID: "Ab12", Name: "Author", Type: typeRichText},
					{ID: "asin-id", Name: "ASIN", Type: typeRichText},
				},
				FieldASIN: {
					{ID: "%3Axs", Name: "Kindle ID", Type: typeRichText},
					{ID: "Ab12", Name: "Author", Type: typeRichText},
					{ID: "asin-id", Name: "ASIN", Type: typeRichText},
				},
				FieldLastAnnotatedAt: {{ID: "date-id", Name: "Last Annotated At", Type: typeDate}},
			},
		},
		{
			name: "名前が合っても型が違う",
			props: []Property{
				{ID: "kindle-number", Name: "Kindle ID", Type: "number"},
				{ID: "author-select", Name: "Author", Type: "select"},
				{ID: "date-created", Name: "Last Annotated At", Type: "created_time"},
			},
			want: map[Field][]Property{},
		},
		{
			name:  "空",
			props: []Property{},
			want:  map[Field][]Property{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Candidates(tt.props); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Candidates() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestDefaultMapping(t *testing.T) {
	tests := []struct {
		name        string
		props       []Property
		want        config.Mapping
		wantMissing []Field
	}{
		{
			name:  "全項目が既定名で決まる",
			props: defaultProperties("Kindle ID", "Author", "ASIN", "Last Annotated At"),
			want:  config.Mapping{Title: "title-id", KindleID: "%3Axs", Author: "Ab12", ASIN: "asin-id", LastAnnotatedAt: "date-id"},
		},
		{
			name:  "kindle_id の表記揺れ: kindle_id",
			props: defaultProperties("kindle_id", "Author", "ASIN", "Last Annotated At"),
			want:  config.Mapping{Title: "title-id", KindleID: "%3Axs", Author: "Ab12", ASIN: "asin-id", LastAnnotatedAt: "date-id"},
		},
		{
			name:  "kindle_id の表記揺れ: 大文字とハイフン",
			props: defaultProperties("KINDLE-ID", "Author", "ASIN", "Last Annotated At"),
			want:  config.Mapping{Title: "title-id", KindleID: "%3Axs", Author: "Ab12", ASIN: "asin-id", LastAnnotatedAt: "date-id"},
		},
		{
			name:  "kindle_id の表記揺れ: Kindle ID",
			props: defaultProperties("Kindle ID", "Author", "ASIN", "Last Annotated At"),
			want:  config.Mapping{Title: "title-id", KindleID: "%3Axs", Author: "Ab12", ASIN: "asin-id", LastAnnotatedAt: "date-id"},
		},
		{
			name:  "kindle_id の表記揺れ: 小文字と空白",
			props: defaultProperties("kindle id", "Author", "ASIN", "Last Annotated At"),
			want:  config.Mapping{Title: "title-id", KindleID: "%3Axs", Author: "Ab12", ASIN: "asin-id", LastAnnotatedAt: "date-id"},
		},
		{
			name:  "kindle_id の表記揺れ: 全角空白",
			props: defaultProperties("Kindle　ID", "Author", "ASIN", "Last Annotated At"),
			want:  config.Mapping{Title: "title-id", KindleID: "%3Axs", Author: "Ab12", ASIN: "asin-id", LastAnnotatedAt: "date-id"},
		},
		{
			name:  "kindle_id の表記揺れ: KindleId",
			props: defaultProperties("KindleId", "Author", "ASIN", "Last Annotated At"),
			want:  config.Mapping{Title: "title-id", KindleID: "%3Axs", Author: "Ab12", ASIN: "asin-id", LastAnnotatedAt: "date-id"},
		},
		{
			name:  "author の表記揺れ: author",
			props: defaultProperties("Kindle ID", "author", "ASIN", "Last Annotated At"),
			want:  config.Mapping{Title: "title-id", KindleID: "%3Axs", Author: "Ab12", ASIN: "asin-id", LastAnnotatedAt: "date-id"},
		},
		{
			name:  "author の表記揺れ: 著者",
			props: defaultProperties("Kindle ID", "著者", "ASIN", "Last Annotated At"),
			want:  config.Mapping{Title: "title-id", KindleID: "%3Axs", Author: "Ab12", ASIN: "asin-id", LastAnnotatedAt: "date-id"},
		},
		{
			name:  "asin の表記揺れ: asin",
			props: defaultProperties("Kindle ID", "Author", "asin", "Last Annotated At"),
			want:  config.Mapping{Title: "title-id", KindleID: "%3Axs", Author: "Ab12", ASIN: "asin-id", LastAnnotatedAt: "date-id"},
		},
		{
			name:  "last_annotated_at の表記揺れ: last_annotated_at",
			props: defaultProperties("Kindle ID", "Author", "ASIN", "last_annotated_at"),
			want:  config.Mapping{Title: "title-id", KindleID: "%3Axs", Author: "Ab12", ASIN: "asin-id", LastAnnotatedAt: "date-id"},
		},
		{
			name:  "last_annotated_at の表記揺れ: Last Annotated",
			props: defaultProperties("Kindle ID", "Author", "ASIN", "Last Annotated"),
			want:  config.Mapping{Title: "title-id", KindleID: "%3Axs", Author: "Ab12", ASIN: "asin-id", LastAnnotatedAt: "date-id"},
		},
		{
			name:  "last_annotated_at の表記揺れ: last-annotated",
			props: defaultProperties("Kindle ID", "Author", "ASIN", "last-annotated"),
			want:  config.Mapping{Title: "title-id", KindleID: "%3Axs", Author: "Ab12", ASIN: "asin-id", LastAnnotatedAt: "date-id"},
		},
		{
			name: "title は名前に関係なく型で決まる",
			props: []Property{
				{ID: "title-id", Name: "Name", Type: typeTitle},
				{ID: "%3Axs", Name: "Kindle ID", Type: typeRichText},
			},
			want: config.Mapping{Title: "title-id", KindleID: "%3Axs"},
		},
		{
			name: "既定名に一致する候補が無い",
			props: []Property{
				{ID: "title-id", Name: "Name", Type: typeTitle},
				{ID: "book-id", Name: "Book ID", Type: typeRichText},
			},
			want:        config.Mapping{Title: "title-id"},
			wantMissing: []Field{FieldKindleID},
		},
		{
			name: "必須項目の既定名一致が複数",
			props: []Property{
				{ID: "title-id", Name: "Name", Type: typeTitle},
				{ID: "%3Axs", Name: "Kindle ID", Type: typeRichText},
				{ID: "kindle-prop", Name: "kindle_id", Type: typeRichText},
			},
			want:        config.Mapping{Title: "title-id"},
			wantMissing: []Field{FieldKindleID},
		},
		{
			name: "任意項目の既定名一致が複数",
			props: []Property{
				{ID: "title-id", Name: "Name", Type: typeTitle},
				{ID: "%3Axs", Name: "Kindle ID", Type: typeRichText},
				{ID: "Ab12", Name: "Author", Type: typeRichText},
				{ID: "author-ja", Name: "著者", Type: typeRichText},
			},
			want: config.Mapping{Title: "title-id", KindleID: "%3Axs"},
		},
		{
			name: "型の合わない Property だけが名前一致",
			props: []Property{
				{ID: "title-id", Name: "Name", Type: typeTitle},
				{ID: "kindle-number", Name: "Kindle ID", Type: "number"},
			},
			want:        config.Mapping{Title: "title-id"},
			wantMissing: []Field{FieldKindleID},
		},
		{
			name: "型違いと型一致が同名正規化で一つずつ",
			props: []Property{
				{ID: "title-id", Name: "Name", Type: typeTitle},
				{ID: "kindle-number", Name: "Kindle ID", Type: "number"},
				{ID: "%3Axs", Name: "kindle_id", Type: typeRichText},
			},
			want: config.Mapping{Title: "title-id", KindleID: "%3Axs"},
		},
		{
			name: "title 型が無い",
			props: []Property{
				{ID: "%3Axs", Name: "Kindle ID", Type: typeRichText},
			},
			want:        config.Mapping{KindleID: "%3Axs"},
			wantMissing: []Field{FieldTitle},
		},
		{
			name: "title と kindle_id が無い",
			props: []Property{
				{ID: "author-id", Name: "Author", Type: typeRichText},
			},
			want:        config.Mapping{Author: "author-id"},
			wantMissing: []Field{FieldTitle, FieldKindleID},
		},
		{
			name: "title 型が複数",
			props: []Property{
				{ID: "title-id", Name: "Name", Type: typeTitle},
				{ID: "title-id-2", Name: "Title", Type: typeTitle},
				{ID: "%3Axs", Name: "Kindle ID", Type: typeRichText},
			},
			want:        config.Mapping{KindleID: "%3Axs"},
			wantMissing: []Field{FieldTitle},
		},
		{
			name:        "空",
			props:       []Property{},
			want:        config.Mapping{},
			wantMissing: []Field{FieldTitle, FieldKindleID},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, missing := DefaultMapping(tt.props)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("DefaultMapping() mapping = %#v, want %#v", got, tt.want)
			}
			if !slices.Equal(missing, tt.wantMissing) {
				t.Errorf("DefaultMapping() missing = %#v, want %#v", missing, tt.wantMissing)
			}
		})
	}
}

func defaultProperties(kindleID, author, asin, lastAnnotatedAt string) []Property {
	return []Property{
		{ID: "title-id", Name: "Name", Type: typeTitle},
		{ID: "%3Axs", Name: kindleID, Type: typeRichText},
		{ID: "Ab12", Name: author, Type: typeRichText},
		{ID: "asin-id", Name: asin, Type: typeRichText},
		{ID: "date-id", Name: lastAnnotatedAt, Type: typeDate},
	}
}
