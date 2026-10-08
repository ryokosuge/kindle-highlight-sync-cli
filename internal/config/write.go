package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

const (
	configDirPerm  os.FileMode = 0o700
	configFilePerm os.FileMode = 0o600
)

// Save は cfg を path に書き出す。親ディレクトリが無ければ 0700 で作り、同じディレクトリの
// 一時ファイル（0600）に書いてから rename で置き換えるので、途中で失敗しても元の path は
// 半端な中身にならない。
func Save(path string, cfg *Config) error {
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, configDirPerm); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	f, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	// fail は本体の error を返しつつ、一時ファイルを close して消す。
	// cleanup の error は本体の error を隠さないよう捨てる。
	fail := func(err error) error {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return fmt.Errorf("%s: %w", path, err)
	}

	if err := f.Chmod(configFilePerm); err != nil {
		return fail(err)
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := os.Rename(f.Name(), path); err != nil {
		_ = os.Remove(f.Name())
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
