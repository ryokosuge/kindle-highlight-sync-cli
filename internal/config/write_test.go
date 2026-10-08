package config

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

var testConfig = Config{
	Amazon: Amazon{Region: "japan"},
	Notion: Notion{
		DatabaseID:   "db-id",
		DataSourceID: "ds-id",
		Mapping: Mapping{
			Title:           "title",
			Author:          "%3Axs",
			ASIN:            "Ab12",
			KindleID:        "Cd34",
			LastAnnotatedAt: "Ef56",
		},
	},
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(ents))
	for i, e := range ents {
		names[i] = e.Name()
	}
	sort.Strings(names)
	return names
}

func TestSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kindle-highlight-sync", "config.toml")
	cfg := testConfig

	if err := Save(path, &cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if *got != cfg {
		t.Errorf("Load() = %+v, want %+v", *got, cfg)
	}
}

func TestSavePerm(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "kindle-highlight-sync")
	path := filepath.Join(parent, "config.toml")
	cfg := testConfig

	if err := Save(path, &cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	fi, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o700 {
		t.Errorf("dir perm = %o, want 0700", got)
	}

	fi, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("file perm = %o, want 0600", got)
	}

	if got := dirEntries(t, parent); len(got) != 1 || got[0] != "config.toml" {
		t.Errorf("dir entries = %v, want [config.toml]", got)
	}
}

func TestSaveReplace(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "正しい config.toml", content: specExample},
		{name: "壊れた config.toml", content: "[amazon\nregion ="},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parent := filepath.Join(t.TempDir(), "kindle-highlight-sync")
			if err := os.MkdirAll(parent, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(parent, "config.toml")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg := testConfig

			if err := Save(path, &cfg); err != nil {
				t.Fatalf("Save() error = %v", err)
			}

			got, err := Load(path)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if *got != cfg {
				t.Errorf("Load() = %+v, want %+v", *got, cfg)
			}

			fi, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := fi.Mode().Perm(); got != 0o600 {
				t.Errorf("file perm = %o, want 0600", got)
			}
		})
	}
}

func TestSaveOmitEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := testConfig
	cfg.Notion.Mapping.Author = ""
	cfg.Notion.Mapping.ASIN = ""
	cfg.Notion.Mapping.LastAnnotatedAt = ""

	if err := Save(path, &cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	mapping, ok := raw["notion"].(map[string]any)["mapping"].(map[string]any)
	if !ok {
		t.Fatalf("notion.mapping = %v, want a table", raw["notion"])
	}
	keys := make([]string, 0, len(mapping))
	for k := range mapping {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{"kindle_id", "title"}
	if len(keys) != len(want) {
		t.Fatalf("notion.mapping keys = %v, want %v", keys, want)
	}
	for i := range keys {
		if keys[i] != want[i] {
			t.Fatalf("notion.mapping keys = %v, want %v", keys, want)
		}
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if *got != cfg {
		t.Errorf("Load() = %+v, want %+v", *got, cfg)
	}
}

func TestSaveError(t *testing.T) {
	t.Run("書き込めないディレクトリ", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.toml")
		original := []byte(specExample)
		if err := os.WriteFile(path, original, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(dir, 0o700); err != nil {
				t.Error(err)
			}
		})
		cfg := testConfig

		if err := Save(path, &cfg); err == nil {
			t.Fatal("Save() error = nil, want error")
		}

		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(original) {
			t.Errorf("config.toml = %q, want it unchanged %q", got, original)
		}
	})

	t.Run("rename に失敗", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.toml")
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "keep"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := testConfig

		if err := Save(path, &cfg); err == nil {
			t.Fatal("Save() error = nil, want error")
		}

		if got := dirEntries(t, dir); len(got) != 1 || got[0] != "config.toml" {
			t.Errorf("dir entries = %v, want [config.toml]", got)
		}
	})
}
