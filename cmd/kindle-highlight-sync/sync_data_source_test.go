package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/state"
)

const (
	syncDataSourceID      = "ds"
	otherSyncDataSourceID = "ds-other-195"
)

func TestSyncDataSource(t *testing.T) {
	t.Run("State の同期先が config.toml と食い違えば、Chrome を起動せず Notion に要求を出さずに exit 6 で終わり、state.db を変えない", func(t *testing.T) {
		tests := []struct {
			name string
			args []string
		}{
			{name: "通常の sync"},
			{name: "sync --dry-run", args: []string{"--dry-run"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				p := setupXDG(t, validConfigTOML)
				requests := stubWritableNotion(t)
				if err := os.MkdirAll(p.DataDir, 0o755); err != nil {
					t.Fatal(err)
				}
				store := state.NewSQLiteStateStore(p.StateFile())
				if err := store.SaveDataSourceID(context.Background(), otherSyncDataSourceID); err != nil {
					t.Fatalf("SaveDataSourceID() error = %v", err)
				}
				if err := store.MarkInitialSyncCompleted(context.Background(), time.Now()); err != nil {
					t.Fatalf("MarkInitialSyncCompleted() error = %v", err)
				}
				before, err := os.ReadFile(p.StateFile())
				if err != nil {
					t.Fatalf("os.ReadFile(%q) error = %v", p.StateFile(), err)
				}
				browser := stubBrowser(t, &fakeBrowser{}, nil)

				code, stderr := runSync(t, tt.args...)
				if code != 6 {
					t.Errorf("run(sync) code = %d, want %d", code, 6)
				}
				wantStderr := fmt.Sprintf("local state / ID scheme error: sync: the Notion data source recorded in %s differs from notion.data_source_id in %s; move %s aside and run sync again\n", p.StateFile(), p.ConfigFile, p.StateFile())
				if stderr != wantStderr {
					t.Errorf("run(sync) stderr = %q, want %q", stderr, wantStderr)
				}
				if browser.opened != 0 {
					t.Errorf("openBrowser calls = %d, want %d", browser.opened, 0)
				}
				assertRequests(t, requests())
				after, err := os.ReadFile(p.StateFile())
				if err != nil {
					t.Fatalf("os.ReadFile(%q) error = %v", p.StateFile(), err)
				}
				if !bytes.Equal(after, before) {
					t.Errorf("state.db bytes after sync = %v, want %v", after, before)
				}
			})
		}
	})

	t.Run("同期先が未記録か一致する State では止まらずに進み、run の後に sync_meta の data_source_id が config.toml の値になっている", func(t *testing.T) {
		tests := []struct {
			name  string
			args  []string
			setup func(t *testing.T) config.Paths
		}{
			{
				name: "state.db が無い",
				args: []string{"--yes"},
				setup: func(t *testing.T) config.Paths {
					p := setupXDG(t, validConfigTOML)
					stubWritableNotion(t)
					return p
				},
			},
			{
				name: "sync_meta に data_source_id が無い",
				setup: func(t *testing.T) config.Paths {
					return setupSyncedXDG(t, validConfigTOML)
				},
			},
			{
				name: "同期先が一致する",
				setup: func(t *testing.T) config.Paths {
					p := setupSyncedXDG(t, validConfigTOML)
					if err := state.NewSQLiteStateStore(p.StateFile()).SaveDataSourceID(context.Background(), syncDataSourceID); err != nil {
						t.Fatalf("SaveDataSourceID() error = %v", err)
					}
					return p
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				p := tt.setup(t)
				_, browser := stubReachedBrowser(t)

				code, stderr := runSync(t, tt.args...)
				if code != 0 {
					t.Errorf("run(sync) code = %d, want %d", code, 0)
				}
				if stderr != "" {
					t.Errorf("run(sync) stderr = %q, want empty", stderr)
				}
				if browser.opened != 1 {
					t.Errorf("openBrowser calls = %d, want %d", browser.opened, 1)
				}

				id, ok, err := state.NewSQLiteStateStore(p.StateFile()).DataSourceID(context.Background())
				if id != syncDataSourceID || !ok || err != nil {
					t.Errorf("DataSourceID() = (%q, %v, %v), want (%q, %v, %v)", id, ok, err, syncDataSourceID, true, nil)
				}
			})
		}
	})
}
