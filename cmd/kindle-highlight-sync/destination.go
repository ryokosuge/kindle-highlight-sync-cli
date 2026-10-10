package main

import (
	"errors"
	"fmt"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/destination"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/secrets"
)

// newDestination は config.toml の [notion] と Notion token から同期先を組み立てる。組み立てでは Notion に要求しない（§41 の順で token を探す）。
func newDestination(a *app) (destination.Destination, error) {
	if a.config == nil {
		return nil, exitcode.New(exitcode.ConfigRequired, fmt.Errorf("%s not found: run `kindle-highlight-sync init` to create it", a.paths.ConfigFile))
	}
	token, _, err := secrets.NotionToken()
	if errors.Is(err, secrets.ErrNotFound) {
		return nil, exitcode.New(exitcode.ConfigRequired, fmt.Errorf("%w: run `kindle-highlight-sync init` to save it", err))
	}
	if err != nil {
		return nil, err // Keychain の読み取り失敗は init と同じく種類を付けない
	}
	return destination.NewNotionDestination(newNotionClient(token), a.config.Notion), nil
}
