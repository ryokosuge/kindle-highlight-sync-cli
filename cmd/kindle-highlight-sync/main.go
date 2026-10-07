package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/redact"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return execute(newRootCmd(), args, stdout, stderr)
}

func execute(root *cobra.Command, args []string, stdout, stderr io.Writer) (code int) {
	defer func() {
		if r := recover(); r != nil {
			code = report(stderr, exitcode.New(exitcode.General, fmt.Errorf("panic: %v", r)))
		}
	}()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	cmd, err := root.ExecuteC()
	if err == nil && cmd == root && !root.HasSubCommands() && len(root.Flags().Args()) > 0 {
		// subcommand を持たない root では cobra の legacyArgs が未知の引数を受け入れて help を出すため、cobra と同じ文言で unknown command にする一時手当て。#23 で sync サブコマンドが入ったら外す。
		err = exitcode.New(exitcode.General, fmt.Errorf("unknown command %q for %q", root.Flags().Args()[0], root.CommandPath()))
	}
	if err != nil {
		return report(stderr, err)
	}
	return 0
}

func report(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintln(stderr, redact.New().String(exitcode.Describe(err)))
	return exitcode.Code(err)
}
