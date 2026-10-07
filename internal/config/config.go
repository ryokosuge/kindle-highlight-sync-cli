// Package config loads config.toml.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Amazon Amazon `toml:"amazon"`
	Notion Notion `toml:"notion"`
}

type Amazon struct {
	Region string `toml:"region"`
}

type Notion struct {
	DatabaseID   string  `toml:"database_id"`
	DataSourceID string  `toml:"data_source_id"`
	Mapping      Mapping `toml:"mapping"`
}

type Mapping struct {
	Title           string `toml:"title"`
	Author          string `toml:"author"`
	ASIN            string `toml:"asin"`
	KindleID        string `toml:"kindle_id"`
	LastAnnotatedAt string `toml:"last_annotated_at"`
}

// Load は path の config.toml を読む。ファイルが無ければ fs.ErrNotExist を包んだ error を返す。
// TOML の誤り・型違い・未知の key は、path と位置（行・列 / key）を含む error にする。
// error に config.toml の値は含めない。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, decodeError(path, err)
	}
	return &cfg, nil
}

func decodeError(path string, err error) error {
	var missing *toml.StrictMissingError
	if errors.As(err, &missing) {
		keys := make([]string, len(missing.Errors))
		for i := range missing.Errors {
			keys[i] = position(&missing.Errors[i])
		}
		return fmt.Errorf("%s: unknown key %s: %w", path, strings.Join(keys, ", "), err)
	}

	var de *toml.DecodeError
	if errors.As(err, &de) {
		return fmt.Errorf("%s: %s: %w", path, position(de), err)
	}

	return fmt.Errorf("%s: %w", path, err)
}

func position(de *toml.DecodeError) string {
	row, col := de.Position()
	if key := de.Key(); len(key) > 0 {
		return fmt.Sprintf("%s (line %d, column %d)", strings.Join(key, "."), row, col)
	}
	return fmt.Sprintf("line %d, column %d", row, col)
}
