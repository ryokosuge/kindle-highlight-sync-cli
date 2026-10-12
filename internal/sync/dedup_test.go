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
				t.Errorf("Candidates() = %v, want %v", got, tt.want)
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
	tests := []struct {
		name         string
		candidates   []domain.Highlight
		remote       map[string]destination.RemoteHighlight
		wantToAppend []domain.Highlight
		wantBackfill []destination.RemoteHighlight
	}{
		{
			name:         "Notion に無いものだけが candidate の順で追記され、Notion にあるものは backfill になる",
			candidates:   dedupHighlights("a-4", "a-3", "a-2", "a-1"),
			remote:       remote,
			wantToAppend: dedupHighlights("a-3", "a-1"),
			wantBackfill: []destination.RemoteHighlight{remote["a-4"], remote["a-2"]},
		},
		{
			name:         "Kindle に無い Notion の既知は backfill に含まれない",
			candidates:   dedupHighlights("a-1"),
			remote:       remote,
			wantToAppend: dedupHighlights("a-1"),
			wantBackfill: nil,
		},
		{
			name:         "Notion の既知が空なら candidate がすべて追記され backfill は空",
			candidates:   dedupHighlights("a-1", "a-2"),
			remote:       nil,
			wantToAppend: dedupHighlights("a-1", "a-2"),
			wantBackfill: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toAppend, backfill := Dedup(tt.candidates, tt.remote)
			if !reflect.DeepEqual(toAppend, tt.wantToAppend) {
				t.Errorf("toAppend = %v, want %v", toAppend, tt.wantToAppend)
			}
			if !reflect.DeepEqual(backfill, tt.wantBackfill) {
				t.Errorf("backfill = %v, want %v", backfill, tt.wantBackfill)
			}
		})
	}
}
