package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const specExample = `[amazon]
region = "japan"

[notion]
database_id = "db-id"
data_source_id = "ds-id"

[notion.mapping]
title = "title"
author = "%3Axs"
asin = "Ab12"
kindle_id = "Cd34"
last_annotated_at = "Ef56"
`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	path := writeConfig(t, specExample)

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := Config{
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
	if *got != want {
		t.Errorf("Load() = %+v, want %+v", *got, want)
	}
}

func TestLoadError(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "TOML として読めない",
			content: "[amazon]\nregion \"japan\"\n",
			want:    "line 2, column 8",
		},
		{
			name:    "型違い",
			content: "[amazon]\nregion = 1\n",
			want:    "amazon.region",
		},
		{
			name:    "mapping の型違い",
			content: "[notion.mapping]\ntitle = [\"title\"]\n",
			want:    "notion.mapping.title",
		},
		{
			name:    "未知の key",
			content: "[notion]\ntoken = \"ntn_abc123\"\n",
			want:    "unknown key notion.token",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.content)

			_, err := Load(path)
			if err == nil {
				t.Fatal("Load() error = nil, want error")
			}
			msg := err.Error()
			for _, s := range []string{path, tt.want} {
				if !strings.Contains(msg, s) {
					t.Errorf("Load() error = %q, want it to contain %q", msg, s)
				}
			}
		})
	}
}

func TestLoadErrorOmitsValue(t *testing.T) {
	path := writeConfig(t, "[notion]\ntoken = \"ntn_abc123\"\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	if strings.Contains(err.Error(), "ntn_abc123") {
		t.Errorf("Load() error = %q, want it not to contain the value", err)
	}
}

func TestLoadNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")

	_, err := Load(path)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Load() error = %v, want fs.ErrNotExist", err)
	}
}
