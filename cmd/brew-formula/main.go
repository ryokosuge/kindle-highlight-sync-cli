// brew-formula は Release の tag と checksums.txt から brew の Formula の本文を標準出力に出す開発用コマンド。
// 使い方: brew-formula <tag> <checksums.txt のパス>。成功は exit 0、失敗は標準エラーに 1 行を出して exit 1。
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/brewformula"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		_, _ = fmt.Fprintln(stderr, "usage: brew-formula <tag> <checksums.txt のパス>")
		return 1
	}
	checksums, err := os.ReadFile(args[1])
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "brew-formula: checksums.txt を読めない: %v\n", err)
		return 1
	}
	out, err := brewformula.Render(args[0], checksums)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "brew-formula: %v\n", err)
		return 1
	}
	if _, err := stdout.Write(out); err != nil {
		_, _ = fmt.Fprintf(stderr, "brew-formula: 標準出力に書けない: %v\n", err)
		return 1
	}
	return 0
}
