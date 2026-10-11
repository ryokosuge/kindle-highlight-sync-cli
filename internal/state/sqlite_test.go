package state

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
)

// localStateCode は §71 の 6（local state / ID scheme error）。exitcode.LocalState と同じ値であることをここで固定する。
const localStateCode = 6

var completedAt = time.Date(2026, 10, 11, 13, 4, 5, 0, time.UTC)

// newTestStore は t.TempDir の中の state.db を指す store とその path を返す。ファイルは作らない。
func newTestStore(t *testing.T) (*SQLiteStateStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	return NewSQLiteStateStore(path), path
}

// execSQL は store を通さずに state.db へ SQL を流す（前提の状態を作るため）。
func execSQL(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := sql.Open(sqliteDriverName, path)
	if err != nil {
		t.Fatalf("sql.Open(%q) error = %v", path, err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close() error = %v", err)
		}
	}()
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("db.Exec(%q) error = %v", stmt, err)
		}
	}
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error = %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestSQLiteStateStore_InitialSyncCompletedAt(t *testing.T) {
	ctx := context.Background()

	t.Run("未記録", func(t *testing.T) {
		tests := []struct {
			name  string
			setup func(t *testing.T, path string)
		}{
			{
				name:  "state.db が無い",
				setup: func(*testing.T, string) {},
			},
			{
				name: "state.db はあるが sync_meta が無い",
				setup: func(t *testing.T, path string) {
					execSQL(t, path, `CREATE TABLE sync_runs (id INTEGER PRIMARY KEY AUTOINCREMENT)`)
				},
			},
			{
				name: "sync_meta はあるが initial_sync_completed_at の行が無い",
				setup: func(t *testing.T, path string) {
					execSQL(t, path, createSyncMetaSQL, `INSERT INTO sync_meta (key, value) VALUES ('schema_version', '1')`)
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				s, path := newTestStore(t)
				tt.setup(t, path)
				before := dirEntries(t, filepath.Dir(path))

				at, ok, err := s.InitialSyncCompletedAt(ctx)
				if err != nil {
					t.Fatalf("InitialSyncCompletedAt() error = %v", err)
				}
				if ok || !at.IsZero() {
					t.Errorf("InitialSyncCompletedAt() = (%v, %v), want (%v, %v)", at, ok, time.Time{}, false)
				}
				// 読むだけでは state.db を作らない（dry-run が state.db を作らない契約の土台）
				if got := dirEntries(t, filepath.Dir(path)); len(got) != len(before) {
					t.Errorf("data dir entries after read = %v, want %v", got, before)
				}
			})
		}
	})

	t.Run("state.db が開けない・読めないときは exit 6 の種類のエラー", func(t *testing.T) {
		tests := []struct {
			name  string
			setup func(t *testing.T, path string)
		}{
			{
				name: "SQLite の形式でないファイル",
				setup: func(t *testing.T, path string) {
					if err := os.WriteFile(path, []byte("state"), 0o600); err != nil {
						t.Fatal(err)
					}
				},
			},
			{
				name: "path がディレクトリ",
				setup: func(t *testing.T, path string) {
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				},
			},
			{
				name: "initial_sync_completed_at が RFC 3339 でない",
				setup: func(t *testing.T, path string) {
					execSQL(t, path, createSyncMetaSQL, `INSERT INTO sync_meta (key, value) VALUES ('initial_sync_completed_at', 'yesterday')`)
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				s, path := newTestStore(t)
				tt.setup(t, path)

				at, ok, err := s.InitialSyncCompletedAt(ctx)
				if err == nil {
					t.Fatalf("InitialSyncCompletedAt() = (%v, %v), error = nil, want an error", at, ok)
				}
				if got := exitcode.Code(err); got != localStateCode {
					t.Errorf("exitcode.Code(err) = %d, want %d (err = %v)", got, localStateCode, err)
				}
				if ok || !at.IsZero() {
					t.Errorf("InitialSyncCompletedAt() = (%v, %v), want (%v, %v) on error", at, ok, time.Time{}, false)
				}
			})
		}
	})
}

func TestSQLiteStateStore_MarkInitialSyncCompleted(t *testing.T) {
	ctx := context.Background()

	t.Run("記録した時刻を別に開き直した store が返す", func(t *testing.T) {
		jst := time.FixedZone("JST", 9*60*60)
		tests := []struct {
			name  string
			setup func(t *testing.T, path string)
			marks []time.Time
			want  time.Time
		}{
			{
				name:  "state.db が無いところへ記録する",
				setup: func(*testing.T, string) {},
				marks: []time.Time{completedAt},
				want:  completedAt,
			},
			{
				name:  "UTC 以外の時刻は UTC にして記録する",
				setup: func(*testing.T, string) {},
				marks: []time.Time{completedAt.In(jst)},
				want:  completedAt,
			},
			{
				name:  "2 回目の記録で上書きする",
				setup: func(*testing.T, string) {},
				marks: []time.Time{completedAt.Add(-24 * time.Hour), completedAt},
				want:  completedAt,
			},
			{
				name: "sync_meta が既にある state.db へ記録する",
				setup: func(t *testing.T, path string) {
					execSQL(t, path, createSyncMetaSQL, `INSERT INTO sync_meta (key, value) VALUES ('schema_version', '1')`)
				},
				marks: []time.Time{completedAt},
				want:  completedAt,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				s, path := newTestStore(t)
				tt.setup(t, path)
				for _, at := range tt.marks {
					if err := s.MarkInitialSyncCompleted(ctx, at); err != nil {
						t.Fatalf("MarkInitialSyncCompleted(%v) error = %v", at, err)
					}
				}

				reopened := NewSQLiteStateStore(path)
				at, ok, err := reopened.InitialSyncCompletedAt(ctx)
				if err != nil {
					t.Fatalf("InitialSyncCompletedAt() error = %v", err)
				}
				if !ok || !at.Equal(tt.want) {
					t.Errorf("InitialSyncCompletedAt() = (%v, %v), want (%v, %v)", at, ok, tt.want, true)
				}
				if at.Location() != time.UTC {
					t.Errorf("InitialSyncCompletedAt().Location() = %v, want %v", at.Location(), time.UTC)
				}
			})
		}
	})

	t.Run("sync_meta の値は RFC 3339 の UTC", func(t *testing.T) {
		s, path := newTestStore(t)
		if err := s.MarkInitialSyncCompleted(ctx, completedAt.In(time.FixedZone("JST", 9*60*60))); err != nil {
			t.Fatalf("MarkInitialSyncCompleted() error = %v", err)
		}
		db, err := sql.Open(sqliteDriverName, path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := db.Close(); err != nil {
				t.Errorf("db.Close() error = %v", err)
			}
		}()
		var value string
		if err := db.QueryRow(selectMetaSQL, "initial_sync_completed_at").Scan(&value); err != nil {
			t.Fatalf("QueryRow() error = %v", err)
		}
		if want := "2026-10-11T13:04:05Z"; value != want {
			t.Errorf("sync_meta.initial_sync_completed_at = %q, want %q", value, want)
		}
	})

	t.Run("state.db が開けない・書けないときは exit 6 の種類のエラー", func(t *testing.T) {
		tests := []struct {
			name  string
			setup func(t *testing.T, path string)
		}{
			{
				name: "SQLite の形式でないファイル",
				setup: func(t *testing.T, path string) {
					if err := os.WriteFile(path, []byte("state"), 0o600); err != nil {
						t.Fatal(err)
					}
				},
			},
			{
				name: "path がディレクトリ",
				setup: func(t *testing.T, path string) {
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				s, path := newTestStore(t)
				tt.setup(t, path)

				err := s.MarkInitialSyncCompleted(ctx, completedAt)
				if err == nil {
					t.Fatal("MarkInitialSyncCompleted() error = nil, want an error")
				}
				if got := exitcode.Code(err); got != localStateCode {
					t.Errorf("exitcode.Code(err) = %d, want %d (err = %v)", got, localStateCode, err)
				}
			})
		}
	})
}
