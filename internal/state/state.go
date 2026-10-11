// Package state は state.db（仕様書 §57 の <data-dir>/kindle-highlight-sync/state.db）に同期の状態を保存する StateStore（§6.3）を持つ。
// Initial Sync の判定（§47）は sync_meta の initial_sync_completed_at の有無だけで行い、state.db や table の有無では行わない。
package state

import (
	"context"
	"time"
)

// StateStore は §6.3 のうち、Initial Sync の判定と完了の記録だけを持つ。
// Book / Highlight の保存の method は使う AC で足す。
type StateStore interface {
	// InitialSyncCompletedAt は sync_meta の initial_sync_completed_at を返す。
	// 未記録（state.db が無い・行が無い）なら ok は false で、エラーにしない。読むだけでは state.db を作らない。
	InitialSyncCompletedAt(ctx context.Context) (at time.Time, ok bool, err error)
	// MarkInitialSyncCompleted は initial_sync_completed_at に at を記録する（あれば上書き）。
	// state.db と sync_meta が無ければ作る。
	MarkInitialSyncCompleted(ctx context.Context, at time.Time) error
}
