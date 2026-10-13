// Package state は state.db（仕様書 §57 の <data-dir>/kindle-highlight-sync/state.db）に同期の状態を保存する StateStore（§6.3）を持つ。
// Initial Sync の判定（§47）は sync_meta の initial_sync_completed_at の有無だけで行い、state.db や table の有無では行わない。
package state

import (
	"context"
	"time"
)

// StateStore は §6.3 のうち、同期先と Initial Sync の判定・完了の記録、同期を終えた Book の記録と読み取り、
// Book ごとの既知の Highlight の読み取りと保存を持つ。
type StateStore interface {
	// InitialSyncCompletedAt は sync_meta の initial_sync_completed_at を返す。
	// 未記録（state.db が無い・行が無い）なら ok は false で、エラーにしない。読むだけでは state.db を作らない。
	InitialSyncCompletedAt(ctx context.Context) (at time.Time, ok bool, err error)
	// MarkInitialSyncCompleted は initial_sync_completed_at に at を記録する（あれば上書き）。
	// state.db と sync_meta が無ければ作る。
	MarkInitialSyncCompleted(ctx context.Context, at time.Time) error
	// DataSourceID は sync_meta の data_source_id（State を書いた同期先の Data Source、§59）を返す。
	// 未記録（state.db が無い・sync_meta が無い・行が無い）なら ok は false で、エラーにしない。読むだけでは state.db を作らない。
	DataSourceID(ctx context.Context) (id string, ok bool, err error)
	// SaveDataSourceID は data_source_id に id を記録する（あれば上書き）。
	// state.db と sync_meta が無ければ作る。
	SaveDataSourceID(ctx context.Context, id string) error
	// ListHighlightIDs は bookID の Book の highlights の行を KindleHighlightID を key にした map で返す。
	// 未記録（state.db が無い・highlights が無い・その Book の行が無い）なら空の map で、エラーにしない。読むだけでは state.db を作らない。
	ListHighlightIDs(ctx context.Context, bookID string) (map[string]HighlightState, error)
	// SaveHighlights は states を 1 つの transaction で highlights に保存する（同じ KindleHighlightID は上書き）。
	// state.db と highlights が無ければ作る。commit 前に失敗したときは batch の行は 1 行も残らない。
	// commit が成功したあとに state.db の Close が失敗したときも exitcode.LocalState（6）のエラーを返すが、行は保存済み。
	// books に行は作らない。foreign_keys は有効にしないので、books に行が無い Book の Highlight も保存できる。
	SaveHighlights(ctx context.Context, states []HighlightState) error
	// MarkBookSynced は books に b を 1 行記録する（同じ KindleBookID は全列を上書き）。
	// state.db と books が無ければ作る。
	MarkBookSynced(ctx context.Context, b BookState) error
	// ListBooks は books の全行を KindleBookID を key にした map で返す。
	// 未記録（state.db が無い・books が無い）なら空の map で、エラーにしない。読むだけでは state.db を作らない。
	// 照合には使わない（照合は Kindle ID Property で行う）。foreign_keys は有効にしない。
	ListBooks(ctx context.Context) (map[string]BookState, error)
}

// BookState は books の 1 行（§59）。
type BookState struct {
	KindleBookID      string
	DestinationBookID string // Notion Page ID
	ASIN              string // 空なら NULL で保存する
	Title             string
	LastAnnotatedAt   *time.Time // nil なら NULL で保存する
	LastSyncedAt      time.Time
}

// HighlightState は highlights の 1 行（§59）。
type HighlightState struct {
	KindleHighlightID      string
	KindleBookID           string
	DestinationHighlightID string // Notion の親 Quote Block の Block ID（destination.RemoteHighlight.ID）
	LastSyncedAt           time.Time
}
