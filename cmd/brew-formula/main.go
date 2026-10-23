// brew-formula は Release の tag と checksums.txt から brew の Formula の本文を標準出力に出す開発用コマンド。
// 使い方: brew-formula [-current <今の定義のパス>] <tag> <checksums.txt のパス>。
// -current があり、tag の版が今の定義より新しい正式版でないときは、今の定義をそのまま出す。
// 成功は exit 0、失敗は標準エラーに 1 行を出して exit 1。
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/brewformula"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("brew-formula", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	currentPath := fs.String("current", "", "今の定義のパス")
	if err := fs.Parse(args); err != nil || fs.NArg() != 2 {
		_, _ = fmt.Fprintln(stderr, "usage: brew-formula [-current <今の定義のパス>] <tag> <checksums.txt のパス>")
		return 1
	}
	tag, checksumsPath := fs.Arg(0), fs.Arg(1)
	checksums, err := os.ReadFile(checksumsPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "brew-formula: checksums.txt を読めない: %v\n", err)
		return 1
	}
	out, err := brewformula.Render(tag, checksums)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "brew-formula: %v\n", err)
		return 1
	}
	if *currentPath != "" {
		current, err := os.ReadFile(*currentPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "brew-formula: -current の定義を読めない: %v\n", err)
			return 1
		}
		update, err := brewformula.ShouldUpdate(current, tag)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "brew-formula: %v\n", err)
			return 1
		}
		if !update {
			out = current
		}
	}
	if _, err := stdout.Write(out); err != nil {
		_, _ = fmt.Fprintf(stderr, "brew-formula: 標準出力に書けない: %v\n", err)
		return 1
	}
	return 0
}
