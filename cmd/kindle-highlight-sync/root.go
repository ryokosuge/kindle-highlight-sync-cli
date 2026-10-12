package main

import (
	"errors"
	"io"
	"io/fs"

	"github.com/spf13/cobra"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/state"
)

// cobraDefaultCmds は cobra が root に足す子コマンド。壊れた config.toml でも動くように前処理を飛ばす。
var cobraDefaultCmds = map[string]bool{"help": true, "completion": true, cobra.ShellCompRequestCmd: true}

// ignoreConfigErrorAnnotation を Annotations に持つ子コマンドでは、前処理の config.toml の読み込み失敗を無視する（a.config は nil）。
const ignoreConfigErrorAnnotation = "kindle-highlight-sync/ignore-config-error"

type app struct {
	paths  config.Paths
	config *config.Config   // config.toml が無ければ nil
	dryRun bool             // sync --dry-run。Notion と State を書かずに予定だけを出す（§63）
	full   bool             // sync --full。State の books の記録に関わらず全 Book を対象にする（§54）
	yes    bool             // sync --yes。Initial Sync の Create / Adopt の確認を省く（§70）
	out    io.Writer        // 予定の表の出力先（sync の RunE で cmd.OutOrStdout() を入れる）
	prompt *prompter        // Initial Sync の確認の入出力（sync の RunE で newSyncPrompter(cmd) を入れる）
	store  state.StateStore // initial_sync_completed_at の読み書き（sync の RunE で newStateStore(a.paths) を入れる）
}

func newRootCmd() *cobra.Command {
	return newRootCmdWith(&app{})
}

func newRootCmdWith(a *app) *cobra.Command {
	root := &cobra.Command{
		Use:           "kindle-highlight-sync",
		Short:         "Kindle の Highlight と Note を Notion Books Database に同期する",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if isCobraDefaultCmd(cmd) {
			return nil
		}
		p, err := config.DefaultPaths()
		if err != nil {
			return err
		}
		if err := p.EnsureDataDir(); err != nil {
			return err
		}
		cfg, err := config.Load(p.ConfigFile)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil && cmd.Annotations[ignoreConfigErrorAnnotation] == "":
			return exitcode.New(exitcode.ConfigRequired, err)
		}
		a.paths, a.config = p, cfg
		return nil
	}
	root.AddCommand(newSyncCmd(a))
	root.AddCommand(newLoginCmd(a))
	root.AddCommand(newInitCmd(a))
	return root
}

// isCobraDefaultCmd は cmd が cobraDefaultCmds（とその子。completion bash など）かを返す。
func isCobraDefaultCmd(cmd *cobra.Command) bool {
	for cmd.HasParent() && cmd.Parent().HasParent() {
		cmd = cmd.Parent()
	}
	return cmd.HasParent() && cobraDefaultCmds[cmd.Name()]
}
