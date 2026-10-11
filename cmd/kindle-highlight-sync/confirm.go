package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	syncengine "github.com/ryokosuge/kindle-highlight-sync-cli/internal/sync"
)

// confirmInitialSync は §47 の Initial Sync の確認をする。nil なら進めてよく、進めないときは exit 3 の種類のエラーを返す。
// yes のときと Create（NewBooks）と Adopt（AdoptableMatches）の合計が 0 のときは問いを出さない。Ambiguous は数えない。
// interactive は入力が端末か（terminalInput(cmd) != nil）。件数だけを出し、Book の title は出さない（§74）。
func confirmInitialSync(p *prompter, s syncengine.Summary, interactive, yes bool) error {
	if yes || s.NewBooks+s.AdoptableMatches == 0 {
		return nil
	}
	if !interactive {
		return exitcode.New(exitcode.ConfigRequired, fmt.Errorf("initial sync will create %d books and adopt %d existing pages: run sync with --yes to confirm without a terminal", s.NewBooks, s.AdoptableMatches))
	}
	if _, err := fmt.Fprintf(p.out, "%d books will be created.\n%d existing pages will be adopted.\n\nContinue? [y/N] ", s.NewBooks, s.AdoptableMatches); err != nil {
		return err
	}
	line, err := p.readLine()
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return err
	}
	if answer := strings.ToLower(line); err == nil && (answer == "y" || answer == "yes") {
		return nil
	}
	return exitcode.New(exitcode.ConfigRequired, errors.New("initial sync was not confirmed"))
}
