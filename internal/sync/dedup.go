package sync

import (
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/destination"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/state"
)

// Candidates は State の既知に Highlight ID が無い Highlight を、hs の順のまま返す（仕様書 §26 の candidate）。
// 空なら State だけで足りるので、Notion の一覧は要らない。
func Candidates(hs []domain.Highlight, local map[string]state.HighlightState) []domain.Highlight {
	return unknownHighlights(hs, local)
}

// Dedup は candidates から Notion の既知（remote）にあるものを除いた追記の対象を、candidates の順のまま返す（§26 の appendIDs）。
// backfill は remote のうち candidates にあるもの、つまり Kindle にあり State に無い Highlight で、State に保存する対象になる（Kindle に無い remote は含めない）。
func Dedup(candidates []domain.Highlight, remote map[string]destination.RemoteHighlight) (toAppend []domain.Highlight, backfill []destination.RemoteHighlight) {
	toAppend = unknownHighlights(candidates, remote)
	for _, h := range candidates {
		if r, ok := remote[h.ID]; ok {
			backfill = append(backfill, r)
		}
	}
	return toAppend, backfill
}
