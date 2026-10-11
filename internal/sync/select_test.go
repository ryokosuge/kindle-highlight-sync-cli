package sync

import (
	"slices"
	"testing"
	"time"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
)

func ids(books []domain.Book) []string {
	out := make([]string, 0, len(books))
	for _, b := range books {
		out = append(out, b.ID)
	}
	return out
}

func TestSelectTargets(t *testing.T) {
	annotated := func(id string, y int, m time.Month, d int) domain.Book {
		at := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
		return domain.Book{ID: id, LastAnnotatedAt: &at}
	}
	// 2026-10-11 08:00 JST = 2026-10-10 23:00 UTC。UTC の暦日は 10-10 で閾値は 10-09。
	// ローカル (JST) の日付を使う間違いと、切り捨てずに 24 時間引く間違いの両方を検出できる値にしてある。
	syncedAt := time.Date(2026, 10, 11, 8, 0, 0, 0, time.FixedZone("JST", 9*60*60))

	tests := []struct {
		name       string
		books      []domain.Book
		lastSynced map[string]time.Time
		full       bool
		want       []string
	}{
		{
			name:       "前回の UTC の日付の前日より前は対象外",
			books:      []domain.Book{annotated("b1", 2026, 10, 8)},
			lastSynced: map[string]time.Time{"b1": syncedAt},
			want:       []string{},
		},
		{
			name:       "前日ちょうどは対象",
			books:      []domain.Book{annotated("b1", 2026, 10, 9)},
			lastSynced: map[string]time.Time{"b1": syncedAt},
			want:       []string{"b1"},
		},
		{
			name:       "前回の UTC の日付と同じ日は対象",
			books:      []domain.Book{annotated("b1", 2026, 10, 10)},
			lastSynced: map[string]time.Time{"b1": syncedAt},
			want:       []string{"b1"},
		},
		{
			name:       "前回の UTC の日付より後は対象",
			books:      []domain.Book{annotated("b1", 2026, 10, 11)},
			lastSynced: map[string]time.Time{"b1": syncedAt},
			want:       []string{"b1"},
		},
		{
			name: "月をまたぐ前日も対象",
			books: []domain.Book{
				annotated("b1", 2026, 10, 30),
				annotated("b2", 2026, 10, 31),
			},
			lastSynced: map[string]time.Time{
				"b1": time.Date(2026, 11, 1, 0, 30, 0, 0, time.UTC),
				"b2": time.Date(2026, 11, 1, 0, 30, 0, 0, time.UTC),
			},
			want: []string{"b2"},
		},
		{
			name:       "last_synced_at の記録が無い Book は対象",
			books:      []domain.Book{annotated("b1", 2020, 1, 1)},
			lastSynced: map[string]time.Time{},
			want:       []string{"b1"},
		},
		{
			// #135 の State の ListBooks は NULL をゼロ値で返す
			name:       "last_synced_at がゼロ値の Book は対象",
			books:      []domain.Book{annotated("b1", 2020, 1, 1)},
			lastSynced: map[string]time.Time{"b1": time.Time{}},
			want:       []string{"b1"},
		},
		{
			name:       "Last Annotated Date が無い Book は対象",
			books:      []domain.Book{{ID: "b1"}},
			lastSynced: map[string]time.Time{"b1": syncedAt},
			want:       []string{"b1"},
		},
		{
			name: "full のときは全 Book を入力順のまま返す",
			books: []domain.Book{
				annotated("b1", 2020, 1, 1),
				{ID: "b2"},
				annotated("b3", 2026, 10, 11),
			},
			lastSynced: map[string]time.Time{"b1": syncedAt},
			full:       true,
			want:       []string{"b1", "b2", "b3"},
		},
		{
			name: "対象の Book だけを入力順のまま返す",
			books: []domain.Book{
				annotated("c", 2026, 10, 10),
				annotated("a", 2026, 10, 8),
				annotated("b", 2026, 10, 11),
				{ID: "d"},
			},
			lastSynced: map[string]time.Time{
				"a": syncedAt,
				"b": syncedAt,
				"c": syncedAt,
				"d": syncedAt,
			},
			want: []string{"c", "b", "d"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SelectTargets(tt.books, tt.lastSynced, tt.full)
			if !slices.Equal(ids(got), tt.want) {
				t.Errorf("SelectTargets() IDs = %v, want %v", ids(got), tt.want)
			}
		})
	}
}
