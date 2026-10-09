package main

import (
	"errors"
	"io/fs"

	"github.com/spf13/cobra"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
)

// cobraDefaultCmds は cobra が root に足す子コマンド。壊れた config.toml でも動くように前処理を飛ばす。
var cobraDefaultCmds = map[string]bool{"help": true, "completion": true, cobra.ShellCompRequestCmd: true}

// ignoreConfigErrorAnnotation を Annotations に持つ子コマンドでは、前処理の config.toml の読み込み失敗を無視する（a.config は nil）。
const ignoreConfigErrorAnnotation = "kindle-highlight-sync/ignore-config-error"

type app struct {
	paths  config.Paths
	config *config.Config // config.toml が無ければ nil
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
