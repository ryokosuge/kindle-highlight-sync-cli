package sync

import (
	"reflect"
	"testing"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/destination"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/state"
)

func dedupHighlights(ids ...string) []domain.Highlight {
	hs := make([]domain.Highlight, 0, len(ids))
	for _, id := range ids {
		hs = append(hs, domain.Highlight{ID: id})
	}
	return hs
}

func TestCandidates(t *testing.T) {
	local := map[string]state.HighlightState{"a-1": {}, "a-2": {}, "a-old": {}}
	tests := []struct {
		name string
		hs   []domain.Highlight
		want []domain.Highlight
	}{
		{"すべて State にあれば candidate は空", dedupHighlights("a-1", "a-2"), nil},
		{"State に無いものだけが Kindle の順で残る", dedupHighlights("a-9", "a-1", "a-3"), dedupHighlights("a-9", "a-3")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Candidates(tt.hs, local); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Candidates = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDedup(t *testing.T) {
	remote := map[string]destination.RemoteHighlight{
		"a-2":        {ID: "blk-2", HighlightID: "a-2"},
		"a-4":        {ID: "blk-4", HighlightID: "a-4"},
		"a-nokindle": {ID: "blk-x", HighlightID: "a-nokindle"},
	}
	candidates := dedupHighlights("a-4", "a-3", "a-2", "a-1")
	toAppend, backfill := Dedup(candidates, remote)
	if want := dedupHighlights("a-3", "a-1"); !reflect.DeepEqual(toAppend, want) {
		t.Errorf("toAppend = %v, want %v", toAppend, want)
	}
	wantBackfill := []destination.RemoteHighlight{remote["a-4"], remote["a-2"]}
	if !reflect.DeepEqual(backfill, wantBackfill) {
		t.Errorf("backfill = %v, want %v", backfill, wantBackfill)
	}
}

func TestDedupRemoteEmpty(t *testing.T) {
	toAppend, backfill := Dedup(dedupHighlights("a-1", "a-2"), nil)
	if want := dedupHighlights("a-1", "a-2"); !reflect.DeepEqual(toAppend, want) {
		t.Errorf("toAppend = %v, want %v", toAppend, want)
	}
	if len(backfill) != 0 {
		t.Errorf("backfill = %v, want empty", backfill)
	}
}
