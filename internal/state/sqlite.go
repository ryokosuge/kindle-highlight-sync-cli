package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	_ "modernc.org/sqlite" // database/sql に "sqlite" driver を登録する（CGO なし。§3.3）

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
)

const (
	sqliteDriverName = "sqlite"

	// keyInitialSyncCompletedAt は sync_meta の key（§59）。値は RFC 3339 の UTC。
	keyInitialSyncCompletedAt = "initial_sync_completed_at"

	createSyncMetaSQL = `CREATE TABLE IF NOT EXISTS sync_meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
)`
	hasSyncMetaSQL = `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'sync_meta'`
	selectMetaSQL  = `SELECT value FROM sync_meta WHERE key = ?`
	upsertMetaSQL  = `INSERT INTO sync_meta (key, value) VALUES (?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value`
)

// SQLiteStateStore は state.db（modernc.org/sqlite）に読み書きする StateStore。
// 接続は操作のたびに開いて閉じるので、呼び出し側に Close の責務は無い。
type SQLiteStateStore struct {
	path string
}

var _ StateStore = (*SQLiteStateStore)(nil)

// NewSQLiteStateStore は path（config.Paths.StateFile()）の state.db を読み書きする store を返す。ファイルには触れない。
func NewSQLiteStateStore(path string) *SQLiteStateStore {
	return &SQLiteStateStore{path: path}
}

// InitialSyncCompletedAt は state.db が無ければ os.Stat だけで未記録を返し、ファイルを作らない。
// state.db が開けない・読めないときは exitcode.LocalState（6）で包んだエラーを返す。
func (s *SQLiteStateStore) InitialSyncCompletedAt(ctx context.Context) (at time.Time, ok bool, err error) {
	if _, err := os.Stat(s.path); errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, false, nil
	} else if err != nil {
		return time.Time{}, false, newStateError(err)
	}

	err = s.withDB(func(db *sql.DB) error {
		var tables int
		if err := db.QueryRowContext(ctx, hasSyncMetaSQL).Scan(&tables); err != nil {
			return fmt.Errorf("read sqlite_master: %w", err)
		}
		if tables == 0 {
			return nil
		}
		var value string
		err := db.QueryRowContext(ctx, selectMetaSQL, keyInitialSyncCompletedAt).Scan(&value)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read sync_meta.%s: %w", keyInitialSyncCompletedAt, err)
		}
		at, err = time.Parse(time.RFC3339, value)
		if err != nil {
			return fmt.Errorf("parse sync_meta.%s: %w", keyInitialSyncCompletedAt, err)
		}
		ok = true
		return nil
	})
	if err != nil {
		return time.Time{}, false, err
	}
	return at, ok, nil
}

// MarkInitialSyncCompleted は at を RFC 3339 の UTC で記録する。失敗は exitcode.LocalState（6）で包む。
func (s *SQLiteStateStore) MarkInitialSyncCompleted(ctx context.Context, at time.Time) error {
	return s.withDB(func(db *sql.DB) error {
		if _, err := db.ExecContext(ctx, createSyncMetaSQL); err != nil {
			return fmt.Errorf("create sync_meta: %w", err)
		}
		value := at.UTC().Format(time.RFC3339)
		if _, err := db.ExecContext(ctx, upsertMetaSQL, keyInitialSyncCompletedAt, value); err != nil {
			return fmt.Errorf("write sync_meta.%s: %w", keyInitialSyncCompletedAt, err)
		}
		return nil
	})
}

// withDB は state.db を開いて body を呼び、閉じる。body の失敗を優先し、body が成功したときだけ Close の失敗を返す。
// どの失敗も exitcode.LocalState で包む。
func (s *SQLiteStateStore) withDB(body func(db *sql.DB) error) (err error) {
	db, err := sql.Open(sqliteDriverName, s.path)
	if err != nil {
		return newStateError(fmt.Errorf("open: %w", err))
	}
	defer func() {
		if cerr := db.Close(); err == nil && cerr != nil {
			err = newStateError(fmt.Errorf("close: %w", cerr))
		}
	}()
	if err := body(db); err != nil {
		return newStateError(err)
	}
	return nil
}

func newStateError(err error) error {
	return exitcode.New(exitcode.LocalState, fmt.Errorf("state.db: %w", err))
}
