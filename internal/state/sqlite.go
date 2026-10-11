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

	// createHighlightsSQL は §59 の highlights の DDL に IF NOT EXISTS を足したもの。
	createHighlightsSQL = `CREATE TABLE IF NOT EXISTS highlights (
    kindle_highlight_id TEXT PRIMARY KEY,
    kindle_book_id TEXT NOT NULL,
    destination_highlight_id TEXT NOT NULL,
    last_synced_at TEXT NOT NULL,
    FOREIGN KEY (kindle_book_id)
      REFERENCES books(kindle_book_id)
)`
	// hasTableSQL は name の table が sqlite_master にあるかを数える。table 名は引数で渡す。
	hasTableSQL = `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`
	// selectHighlightsSQL は 1 冊の Book の highlights の全行を読む。
	selectHighlightsSQL = `SELECT kindle_highlight_id, kindle_book_id, destination_highlight_id, last_synced_at FROM highlights WHERE kindle_book_id = ?`
	upsertHighlightSQL  = `INSERT INTO highlights (kindle_highlight_id, kindle_book_id, destination_highlight_id, last_synced_at) VALUES (?, ?, ?, ?)
ON CONFLICT (kindle_highlight_id) DO UPDATE SET kindle_book_id = excluded.kindle_book_id, destination_highlight_id = excluded.destination_highlight_id, last_synced_at = excluded.last_synced_at`
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

// ListHighlightIDs は state.db が無ければ os.Stat だけで空の map を返し、ファイルを作らない。
// state.db が開けない・読めないときは exitcode.LocalState（6）で包んだエラーを返す。
func (s *SQLiteStateStore) ListHighlightIDs(ctx context.Context, bookID string) (map[string]HighlightState, error) {
	exists, err := s.stateFileExists()
	if err != nil {
		return nil, err
	}
	if !exists {
		return map[string]HighlightState{}, nil
	}

	var states map[string]HighlightState
	err = s.withDB(func(db *sql.DB) error {
		ok, err := hasTable(ctx, db, "highlights")
		if err != nil {
			return err
		}
		if !ok {
			states = map[string]HighlightState{}
			return nil
		}
		rows, err := db.QueryContext(ctx, selectHighlightsSQL, bookID)
		if err != nil {
			return fmt.Errorf("read highlights: %w", err)
		}
		defer func() { _ = rows.Close() }()
		states = map[string]HighlightState{}
		for rows.Next() {
			var st HighlightState
			var lastSyncedAt string
			if err := rows.Scan(&st.KindleHighlightID, &st.KindleBookID, &st.DestinationHighlightID, &lastSyncedAt); err != nil {
				return fmt.Errorf("read highlights: %w", err)
			}
			st.LastSyncedAt, err = time.Parse(time.RFC3339, lastSyncedAt)
			if err != nil {
				return fmt.Errorf("parse highlights.last_synced_at of %s: %w", st.KindleHighlightID, err)
			}
			states[st.KindleHighlightID] = st
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("read highlights: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return states, nil
}

// SaveHighlights は states を 1 つの transaction で保存する。失敗は exitcode.LocalState（6）で包み、batch の行は残さない。
func (s *SQLiteStateStore) SaveHighlights(ctx context.Context, states []HighlightState) error {
	return s.withDB(func(db *sql.DB) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin: %w", err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(ctx, createHighlightsSQL); err != nil {
			return fmt.Errorf("create highlights: %w", err)
		}
		for _, st := range states {
			if st.DestinationHighlightID == "" {
				return fmt.Errorf("save highlight %s: destination highlight ID is empty", st.KindleHighlightID)
			}
			if _, err := tx.ExecContext(ctx, upsertHighlightSQL, st.KindleHighlightID, st.KindleBookID, st.DestinationHighlightID, st.LastSyncedAt.UTC().Format(time.RFC3339)); err != nil {
				return fmt.Errorf("save highlight %s: %w", st.KindleHighlightID, err)
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit: %w", err)
		}
		return nil
	})
}

// stateFileExists は state.db が在るかを返す。読むだけの操作が state.db を作らないための判定。
func (s *SQLiteStateStore) stateFileExists() (bool, error) {
	if _, err := os.Stat(s.path); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, newStateError(err)
	}
	return true, nil
}

// hasTable は name の table が state.db にあるかを返す。
func hasTable(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var tables int
	if err := db.QueryRowContext(ctx, hasTableSQL, name).Scan(&tables); err != nil {
		return false, fmt.Errorf("read sqlite_master: %w", err)
	}
	return tables > 0, nil
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
