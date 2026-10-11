package main

import (
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/state"
)

// newStateStore は sync が読み書きする State を開く。既定は p.StateFile() の state.db（§57）で、開くだけではファイルに触れない。
// テストは記録の失敗を起こす store に差し替える。
var newStateStore = func(p config.Paths) state.StateStore { return state.NewSQLiteStateStore(p.StateFile()) }
