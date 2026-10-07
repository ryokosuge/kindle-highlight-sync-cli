package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
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
			name:    "inline table の型違い",
			content: "notion = { mapping = { title = 1 } }\n",
			want:    "notion.mapping.title",
		},
		{
			name:    "範囲外の浮動小数点",
			content: "[amazon]\nregion = 1e999\n",
			want:    "amazon.region",
		},
		{
			name:    "未知の key",
			content: "[notion]\ntoken = \"ntn_abc123\"\n",
			want:    "unknown key notion.token",
		},
		{
			name:    "大文字小文字だけ違う key",
			content: "[amazon]\nREGION = \"japan\"\n",
			want:    "unknown key amazon.REGION",
		},
		{
			name:    "大文字小文字だけ違う key の併記",
			content: "[amazon]\nregion = \"japan\"\nREGION = \"us\"\n",
			want:    "unknown key amazon.REGION",
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
	tests := []struct {
		name    string
		content string
		value   string
	}{
		{
			name:    "未知の key",
			content: "[notion]\ntoken = \"ntn_abc123\"\n",
			value:   "ntn_abc123",
		},
		{
			name:    "範囲外の浮動小数点",
			content: "[amazon]\nregion = 1e999\n",
			value:   "1e999",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.content)

			_, err := Load(path)
			if err == nil {
				t.Fatal("Load() error = nil, want error")
			}
			if strings.Contains(err.Error(), tt.value) {
				t.Errorf("Load() error = %q, want it not to contain %q", err, tt.value)
			}
		})
	}
}

func TestLoadErrorAs(t *testing.T) {
	t.Run("型違いは *toml.DecodeError", func(t *testing.T) {
		_, err := Load(writeConfig(t, "[amazon]\nregion = 1\n"))
		var de *toml.DecodeError
		if !errors.As(err, &de) {
			t.Errorf("Load() error = %v, want *toml.DecodeError", err)
		}
	})
	t.Run("未知の key は *toml.StrictMissingError", func(t *testing.T) {
		_, err := Load(writeConfig(t, "[notion]\ntoken = \"ntn_abc123\"\n"))
		var missing *toml.StrictMissingError
		if !errors.As(err, &missing) {
			t.Errorf("Load() error = %v, want *toml.StrictMissingError", err)
		}
	})
}

func TestLoadNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")

	_, err := Load(path)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Load() error = %v, want fs.ErrNotExist", err)
	}
}
