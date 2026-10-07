package main

import "github.com/spf13/cobra"

func newRootCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "kindle-highlight-sync",
		Short:         "Kindle の Highlight と Note を Notion Books Database に同期する",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
}
