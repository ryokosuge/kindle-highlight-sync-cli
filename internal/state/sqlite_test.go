package state

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
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

// countRows は table の行数を store を通さずに数える。
func countRows(t *testing.T, path, table string) int {
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
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s error = %v", table, err)
	}
	return n
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

func TestSQLiteStateStore_ListHighlightIDs(t *testing.T) {
	ctx := context.Background()
	bookID := "B0BOOKAAAA"
	otherBookID := "B0BOOKBBBB"

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
				name: "state.db はあるが highlights が無い",
				setup: func(t *testing.T, path string) {
					execSQL(t, path, `CREATE TABLE sync_runs (id INTEGER PRIMARY KEY AUTOINCREMENT)`)
				},
			},
			{
				name: "highlights に別の Book の行だけある",
				setup: func(t *testing.T, path string) {
					execSQL(t, path, createHighlightsSQL,
						`INSERT INTO highlights (kindle_highlight_id, kindle_book_id, destination_highlight_id, last_synced_at) VALUES ('a-9999', '`+otherBookID+`', 'd0d0d0d0-0000-0000-0000-000000000009', '2026-10-11T13:04:05Z')`)
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				s, path := newTestStore(t)
				tt.setup(t, path)
				before := dirEntries(t, filepath.Dir(path))

				states, err := s.ListHighlightIDs(ctx, bookID)
				if err != nil {
					t.Fatalf("ListHighlightIDs() error = %v", err)
				}
				if len(states) != 0 {
					t.Errorf("ListHighlightIDs() = %v, want an empty map", states)
				}
				// 読むだけでは state.db を作らない
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
				name: "last_synced_at が RFC 3339 でない",
				setup: func(t *testing.T, path string) {
					execSQL(t, path, createHighlightsSQL,
						`INSERT INTO highlights (kindle_highlight_id, kindle_book_id, destination_highlight_id, last_synced_at) VALUES ('a-0001', '`+bookID+`', 'd0d0d0d0-0000-0000-0000-000000000001', 'yesterday')`)
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				s, path := newTestStore(t)
				tt.setup(t, path)

				states, err := s.ListHighlightIDs(ctx, bookID)
				if err == nil {
					t.Fatalf("ListHighlightIDs() = %v, error = nil, want an error", states)
				}
				if got := exitcode.Code(err); got != localStateCode {
					t.Errorf("exitcode.Code(err) = %d, want %d (err = %v)", got, localStateCode, err)
				}
				if states != nil {
					t.Errorf("ListHighlightIDs() = %v, want nil on error", states)
				}
			})
		}
	})
}

func TestSQLiteStateStore_SaveHighlights(t *testing.T) {
	ctx := context.Background()
	bookID := "B0BOOKAAAA"
	otherBookID := "B0BOOKBBBB"
	highlightA := HighlightState{
		KindleHighlightID:      "a-0001",
		KindleBookID:           bookID,
		DestinationHighlightID: "d0d0d0d0-0000-0000-0000-000000000001",
		LastSyncedAt:           completedAt,
	}
	highlightA2 := HighlightState{
		KindleHighlightID:      "a-0002",
		KindleBookID:           bookID,
		DestinationHighlightID: "d0d0d0d0-0000-0000-0000-000000000002",
		LastSyncedAt:           completedAt.Add(-time.Hour),
	}
	highlightB := HighlightState{
		KindleHighlightID:      "a-1001",
		KindleBookID:           otherBookID,
		DestinationHighlightID: "d0d0d0d0-0000-0000-0000-000000001001",
		LastSyncedAt:           completedAt,
	}

	t.Run("保存した行を別に開き直した store が返す", func(t *testing.T) {
		jst := time.FixedZone("JST", 9*60*60)
		tests := []struct {
			name  string
			setup func(t *testing.T, path string)
			saves [][]HighlightState
			want  map[string]HighlightState
		}{
			{
				name:  "state.db が無いところへ保存する",
				setup: func(*testing.T, string) {},
				saves: [][]HighlightState{{highlightA, highlightA2, highlightB}},
				want:  map[string]HighlightState{"a-0001": highlightA, "a-0002": highlightA2},
			},
			{
				name:  "同じ Highlight ID を 2 回保存すると上書きし行は 1 つのまま",
				setup: func(*testing.T, string) {},
				saves: [][]HighlightState{
					{highlightA},
					{{
						KindleHighlightID:      highlightA.KindleHighlightID,
						KindleBookID:           highlightA.KindleBookID,
						DestinationHighlightID: "d0d0d0d0-0000-0000-0000-00000000000a",
						LastSyncedAt:           completedAt.Add(time.Hour),
					}},
				},
				want: map[string]HighlightState{"a-0001": {
					KindleHighlightID:      highlightA.KindleHighlightID,
					KindleBookID:           highlightA.KindleBookID,
					DestinationHighlightID: "d0d0d0d0-0000-0000-0000-00000000000a",
					LastSyncedAt:           completedAt.Add(time.Hour),
				}},
			},
			{
				name:  "books が無い Book ID の Highlight も保存できる",
				setup: func(*testing.T, string) {},
				saves: [][]HighlightState{{highlightA}},
				want:  map[string]HighlightState{"a-0001": highlightA},
			},
			{
				name: "books テーブルはあるが行が無い Book ID の Highlight も保存できる",
				setup: func(t *testing.T, path string) {
					execSQL(t, path, `CREATE TABLE books (
    kindle_book_id TEXT PRIMARY KEY,
    destination_book_id TEXT NOT NULL,
    asin TEXT,
    title TEXT NOT NULL,
    last_annotated_at TEXT,
    last_synced_at TEXT
)`)
				},
				saves: [][]HighlightState{{highlightA}},
				want:  map[string]HighlightState{"a-0001": highlightA},
			},
			{
				name:  "UTC 以外の LastSyncedAt は UTC で返る",
				setup: func(*testing.T, string) {},
				saves: [][]HighlightState{{{
					KindleHighlightID:      highlightA.KindleHighlightID,
					KindleBookID:           highlightA.KindleBookID,
					DestinationHighlightID: highlightA.DestinationHighlightID,
					LastSyncedAt:           completedAt.In(jst),
				}}},
				want: map[string]HighlightState{"a-0001": highlightA},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				s, path := newTestStore(t)
				tt.setup(t, path)
				for _, states := range tt.saves {
					if err := s.SaveHighlights(ctx, states); err != nil {
						t.Fatalf("SaveHighlights(%v) error = %v", states, err)
					}
				}

				reopened := NewSQLiteStateStore(path)
				got, err := reopened.ListHighlightIDs(ctx, bookID)
				if err != nil {
					t.Fatalf("ListHighlightIDs() error = %v", err)
				}
				if !reflect.DeepEqual(got, tt.want) {
					t.Errorf("ListHighlightIDs() = %v, want %v", got, tt.want)
				}
				// 行は upsert なので、異なる Book の行を含めて一意な Highlight ID の数だけ残る
				unique := map[string]bool{}
				for _, states := range tt.saves {
					for _, st := range states {
						unique[st.KindleHighlightID] = true
					}
				}
				if n := countRows(t, path, "highlights"); n != len(unique) {
					t.Errorf("countRows(highlights) = %d, want %d", n, len(unique))
				}
			})
		}
	})

	t.Run("last_synced_at の値は RFC 3339 の UTC", func(t *testing.T) {
		s, path := newTestStore(t)
		st := highlightA
		st.LastSyncedAt = completedAt.In(time.FixedZone("JST", 9*60*60))
		if err := s.SaveHighlights(ctx, []HighlightState{st}); err != nil {
			t.Fatalf("SaveHighlights() error = %v", err)
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
		if err := db.QueryRow(`SELECT last_synced_at FROM highlights WHERE kindle_highlight_id = ?`, st.KindleHighlightID).Scan(&value); err != nil {
			t.Fatalf("QueryRow() error = %v", err)
		}
		if want := "2026-10-11T13:04:05Z"; value != want {
			t.Errorf("highlights.last_synced_at = %q, want %q", value, want)
		}
	})

	t.Run("DestinationHighlightID が 1 つでも空なら 1 行も増えない", func(t *testing.T) {
		t.Run("既存の行も変わらない", func(t *testing.T) {
			s, path := newTestStore(t)
			if err := s.SaveHighlights(ctx, []HighlightState{highlightA}); err != nil {
				t.Fatalf("SaveHighlights() error = %v", err)
			}

			y := HighlightState{
				KindleHighlightID:      "a-0002",
				KindleBookID:           bookID,
				DestinationHighlightID: "d0d0d0d0-0000-0000-0000-000000000002",
				LastSyncedAt:           completedAt,
			}
			changed := highlightA
			changed.DestinationHighlightID = "d0d0d0d0-0000-0000-0000-00000000000a"
			z := HighlightState{
				KindleHighlightID: "a-0003",
				KindleBookID:      bookID,
				LastSyncedAt:      completedAt,
			}
			if err := s.SaveHighlights(ctx, []HighlightState{y, changed, z}); err == nil {
				t.Fatal("SaveHighlights() error = nil, want an error")
			}

			got, err := NewSQLiteStateStore(path).ListHighlightIDs(ctx, bookID)
			if err != nil {
				t.Fatalf("ListHighlightIDs() error = %v", err)
			}
			want := map[string]HighlightState{highlightA.KindleHighlightID: highlightA}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("ListHighlightIDs() = %v, want %v", got, want)
			}
			if n := countRows(t, path, "highlights"); n != 1 {
				t.Errorf("countRows(highlights) = %d, want %d", n, 1)
			}
		})

		t.Run("state.db が無いところでも 1 行も増えない", func(t *testing.T) {
			s, path := newTestStore(t)
			z := HighlightState{
				KindleHighlightID: "a-0003",
				KindleBookID:      bookID,
				LastSyncedAt:      completedAt,
			}
			if err := s.SaveHighlights(ctx, []HighlightState{highlightA, z}); err == nil {
				t.Fatal("SaveHighlights() error = nil, want an error")
			}
			got, err := NewSQLiteStateStore(path).ListHighlightIDs(ctx, bookID)
			if err != nil {
				t.Fatalf("ListHighlightIDs() error = %v", err)
			}
			if len(got) != 0 {
				t.Errorf("ListHighlightIDs() = %v, want an empty map", got)
			}
		})
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

				err := s.SaveHighlights(ctx, []HighlightState{highlightA})
				if err == nil {
					t.Fatal("SaveHighlights() error = nil, want an error")
				}
				if got := exitcode.Code(err); got != localStateCode {
					t.Errorf("exitcode.Code(err) = %d, want %d (err = %v)", got, localStateCode, err)
				}
			})
		}
	})
}

func TestSQLiteStateStore_ListBooks(t *testing.T) {
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
				name: "state.db はあるが books が無い",
				setup: func(t *testing.T, path string) {
					execSQL(t, path, createHighlightsSQL)
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				s, path := newTestStore(t)
				tt.setup(t, path)
				before := dirEntries(t, filepath.Dir(path))

				books, err := s.ListBooks(ctx)
				if err != nil {
					t.Fatalf("ListBooks() error = %v", err)
				}
				if len(books) != 0 {
					t.Errorf("ListBooks() = %v, want an empty map", books)
				}
				// 読むだけでは state.db を作らない
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
				name: "last_synced_at が RFC 3339 でない",
				setup: func(t *testing.T, path string) {
					execSQL(t, path, createBooksSQL,
						`INSERT INTO books (kindle_book_id, destination_book_id, asin, title, last_annotated_at, last_synced_at) VALUES ('B0BOOKAAAA', 'page-1', 'B0BOOKAAAA', 'title', '2026-10-11T00:00:00Z', 'yesterday')`)
				},
			},
			{
				name: "last_annotated_at が RFC 3339 でない",
				setup: func(t *testing.T, path string) {
					execSQL(t, path, createBooksSQL,
						`INSERT INTO books (kindle_book_id, destination_book_id, asin, title, last_annotated_at, last_synced_at) VALUES ('B0BOOKAAAA', 'page-1', 'B0BOOKAAAA', 'title', 'yesterday', '2026-10-11T13:04:05Z')`)
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				s, path := newTestStore(t)
				tt.setup(t, path)

				books, err := s.ListBooks(ctx)
				if err == nil {
					t.Fatalf("ListBooks() = %v, error = nil, want an error", books)
				}
				if got := exitcode.Code(err); got != localStateCode {
					t.Errorf("exitcode.Code(err) = %d, want %d (err = %v)", got, localStateCode, err)
				}
				if books != nil {
					t.Errorf("ListBooks() = %v, want nil on error", books)
				}
			})
		}
	})
}

func TestSQLiteStateStore_MarkBookSynced(t *testing.T) {
	ctx := context.Background()
	bookID := "B0BOOKAAAA"
	lastAnnotatedAt := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC) // internal/source/library.go と同じく日付の 00:00 UTC
	book := BookState{
		KindleBookID:      bookID,
		DestinationBookID: "d0d0d0d0-0000-0000-0000-0000000000b1",
		ASIN:              bookID,
		Title:             "Go 言語による並行処理",
		LastAnnotatedAt:   &lastAnnotatedAt,
		LastSyncedAt:      completedAt,
	}

	t.Run("記録した行を別に開き直した store が返す", func(t *testing.T) {
		jst := time.FixedZone("JST", 9*60*60)
		tests := []struct {
			name  string
			marks []BookState
			want  map[string]BookState
		}{
			{
				name:  "state.db が無いところへ記録する",
				marks: []BookState{book},
				want:  map[string]BookState{bookID: book},
			},
			{
				name: "2 回記録すると全列を上書きし行は 1 つのまま",
				marks: []BookState{book, {
					KindleBookID:      bookID,
					DestinationBookID: "d0d0d0d0-0000-0000-0000-0000000000b2",
					ASIN:              bookID,
					Title:             "Go 言語による並行処理 第 2 版",
					LastAnnotatedAt:   &lastAnnotatedAt,
					LastSyncedAt:      completedAt.Add(time.Hour),
				}},
				want: map[string]BookState{bookID: {
					KindleBookID:      bookID,
					DestinationBookID: "d0d0d0d0-0000-0000-0000-0000000000b2",
					ASIN:              bookID,
					Title:             "Go 言語による並行処理 第 2 版",
					LastAnnotatedAt:   &lastAnnotatedAt,
					LastSyncedAt:      completedAt.Add(time.Hour),
				}},
			},
			{
				name:  "UTC 以外の LastSyncedAt は UTC で返る",
				marks: []BookState{{KindleBookID: bookID, DestinationBookID: book.DestinationBookID, ASIN: bookID, Title: book.Title, LastAnnotatedAt: &lastAnnotatedAt, LastSyncedAt: completedAt.In(jst)}},
				want:  map[string]BookState{bookID: book},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				s, path := newTestStore(t)
				for _, b := range tt.marks {
					if err := s.MarkBookSynced(ctx, b); err != nil {
						t.Fatalf("MarkBookSynced(%v) error = %v", b, err)
					}
				}

				reopened := NewSQLiteStateStore(path)
				got, err := reopened.ListBooks(ctx)
				if err != nil {
					t.Fatalf("ListBooks() error = %v", err)
				}
				if !reflect.DeepEqual(got, tt.want) {
					t.Errorf("ListBooks() = %v, want %v", got, tt.want)
				}
				if n := countRows(t, path, "books"); n != len(tt.want) {
					t.Errorf("countRows(books) = %d, want %d", n, len(tt.want))
				}
			})
		}
	})

	t.Run("ASIN と LastAnnotatedAt が無い行は NULL で保存し、読むと空と nil で返る", func(t *testing.T) {
		s, path := newTestStore(t)
		b := BookState{
			KindleBookID:      bookID,
			DestinationBookID: book.DestinationBookID,
			Title:             book.Title,
			LastSyncedAt:      completedAt,
		}
		if err := s.MarkBookSynced(ctx, b); err != nil {
			t.Fatalf("MarkBookSynced() error = %v", err)
		}

		got, err := NewSQLiteStateStore(path).ListBooks(ctx)
		if err != nil {
			t.Fatalf("ListBooks() error = %v", err)
		}
		want := map[string]BookState{bookID: b}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ListBooks() = %v, want %v", got, want)
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
		var nulls int
		if err := db.QueryRow(`SELECT count(*) FROM books WHERE kindle_book_id = ? AND asin IS NULL AND last_annotated_at IS NULL`, bookID).Scan(&nulls); err != nil {
			t.Fatalf("QueryRow() error = %v", err)
		}
		if nulls != 1 {
			t.Errorf("rows with asin IS NULL AND last_annotated_at IS NULL = %d, want %d", nulls, 1)
		}
	})

	t.Run("last_synced_at と last_annotated_at の値は RFC 3339 の UTC", func(t *testing.T) {
		s, path := newTestStore(t)
		jst := time.FixedZone("JST", 9*60*60)
		annotatedJST := lastAnnotatedAt.In(jst)
		b := book
		b.LastAnnotatedAt = &annotatedJST
		b.LastSyncedAt = completedAt.In(jst)
		if err := s.MarkBookSynced(ctx, b); err != nil {
			t.Fatalf("MarkBookSynced() error = %v", err)
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
		var syncedAt, annotatedAt string
		if err := db.QueryRow(`SELECT last_synced_at, last_annotated_at FROM books WHERE kindle_book_id = ?`, bookID).Scan(&syncedAt, &annotatedAt); err != nil {
			t.Fatalf("QueryRow() error = %v", err)
		}
		if want := "2026-10-11T13:04:05Z"; syncedAt != want {
			t.Errorf("books.last_synced_at = %q, want %q", syncedAt, want)
		}
		if want := "2026-10-11T00:00:00Z"; annotatedAt != want {
			t.Errorf("books.last_annotated_at = %q, want %q", annotatedAt, want)
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

				err := s.MarkBookSynced(ctx, book)
				if err == nil {
					t.Fatal("MarkBookSynced() error = nil, want an error")
				}
				if got := exitcode.Code(err); got != localStateCode {
					t.Errorf("exitcode.Code(err) = %d, want %d (err = %v)", got, localStateCode, err)
				}
			})
		}
	})
}
