package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// prompter は init の問いと選択肢を cmd の標準エラーに出し、cmd の入力から答えを読む。
type prompter struct {
	in   *bufio.Reader
	term *os.File
	out  io.Writer
}

func newPrompter(cmd *cobra.Command) *prompter {
	in := cmd.InOrStdin()
	var termFile *os.File
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		termFile = f
	}
	return &prompter{in: bufio.NewReader(in), term: termFile, out: cmd.ErrOrStderr()}
}

func (p *prompter) line(question string) (string, error) {
	if _, err := fmt.Fprintf(p.out, "%s: ", question); err != nil {
		return "", err
	}
	return p.readLine()
}

func (p *prompter) secret(question string) (string, error) {
	if _, err := fmt.Fprintf(p.out, "%s: ", question); err != nil {
		return "", err
	}
	if p.term == nil {
		return p.readLine()
	}
	password, readErr := term.ReadPassword(int(p.term.Fd()))
	_, writeErr := fmt.Fprintln(p.out)
	if readErr != nil {
		return "", readErr
	}
	if writeErr != nil {
		return "", writeErr
	}
	return strings.TrimSpace(string(password)), nil
}

func (p *prompter) readLine() (string, error) {
	line, err := p.in.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	if err == io.EOF && len(line) == 0 {
		return "", io.ErrUnexpectedEOF
	}
	return strings.TrimSpace(line), nil
}

const (
	noDefault = -1 // choose の def に渡す「既定なし」
	notMapped = -2 // 「対応させない」。choose が返し、optional のときは def にも渡せる
)

// choose は番号で選択させる。def に notMapped を渡せるのは optional のときだけ。
func (p *prompter) choose(question string, options []string, def int, optional bool) (int, error) {
	if _, err := fmt.Fprintln(p.out, question); err != nil {
		return 0, err
	}
	for i, option := range options {
		if _, err := fmt.Fprintf(p.out, "  %d) %s\n", i+1, option); err != nil {
			return 0, err
		}
	}
	if optional {
		if _, err := fmt.Fprintln(p.out, "  0) do not map"); err != nil {
			return 0, err
		}
	}

	min := 1
	if optional {
		min = 0
	}
	for {
		switch def {
		case noDefault:
			if _, err := fmt.Fprint(p.out, "number: "); err != nil {
				return 0, err
			}
		case notMapped:
			if _, err := fmt.Fprint(p.out, "number [0]: "); err != nil {
				return 0, err
			}
		default:
			if _, err := fmt.Fprintf(p.out, "number [%d]: ", def+1); err != nil {
				return 0, err
			}
		}
		line, err := p.readLine()
		if err != nil {
			return 0, err
		}
		if line == "" && def != noDefault {
			return def, nil
		}
		n, err := strconv.Atoi(line)
		if err == nil && n >= 1 && n <= len(options) {
			return n - 1, nil
		}
		if optional && err == nil && n == 0 {
			return notMapped, nil
		}
		if _, err := fmt.Fprintf(p.out, "enter a number from %d to %d\n", min, len(options)); err != nil {
			return 0, err
		}
	}
}
