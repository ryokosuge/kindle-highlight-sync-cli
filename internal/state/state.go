// Package state は state.db（仕様書 §57 の <data-dir>/kindle-highlight-sync/state.db）に同期の状態を保存する StateStore（§6.3）を持つ。
// Initial Sync の判定（§47）は sync_meta の initial_sync_completed_at の有無だけで行い、state.db や table の有無では行わない。
package state

import (
	"context"
	"time"
)

// StateStore は §6.3 のうち、Initial Sync の判定と完了の記録と、Book ごとの既知の Highlight の読み取りと保存を持つ。
// Book の記録の method は使う AC で足す。
type StateStore interface {
	// InitialSyncCompletedAt は sync_meta の initial_sync_completed_at を返す。
	// 未記録（state.db が無い・行が無い）なら ok は false で、エラーにしない。読むだけでは state.db を作らない。
	InitialSyncCompletedAt(ctx context.Context) (at time.Time, ok bool, err error)
	// MarkInitialSyncCompleted は initial_sync_completed_at に at を記録する（あれば上書き）。
	// state.db と sync_meta が無ければ作る。
	MarkInitialSyncCompleted(ctx context.Context, at time.Time) error
	// ListHighlightIDs は bookID の Book の highlights の行を KindleHighlightID を key にした map で返す。
	// 未記録（state.db が無い・highlights が無い・その Book の行が無い）なら空の map で、エラーにしない。読むだけでは state.db を作らない。
	ListHighlightIDs(ctx context.Context, bookID string) (map[string]HighlightState, error)
	// SaveHighlights は states を 1 つの transaction で highlights に保存する（同じ KindleHighlightID は上書き）。
	// state.db と highlights が無ければ作る。commit 前に失敗したときは batch の行は 1 行も残らない。
	// commit が成功したあとに state.db の Close が失敗したときも exitcode.LocalState（6）のエラーを返すが、行は保存済み。
	// books に行は作らない。foreign_keys は有効にしないので、books に行が無い Book の Highlight も保存できる。
	SaveHighlights(ctx context.Context, states []HighlightState) error
}

// HighlightState は highlights の 1 行（§59）。
type HighlightState struct {
	KindleHighlightID      string
	KindleBookID           string
	DestinationHighlightID string // Notion の親 Quote Block の Block ID（destination.RemoteHighlight.ID）
	LastSyncedAt           time.Time
}
