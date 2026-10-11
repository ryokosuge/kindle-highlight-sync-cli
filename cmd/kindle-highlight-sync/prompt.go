package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// prompter は init の問いと選択肢を cmd の標準エラーに出し、cmd の入力から答えを読む。
type prompter struct {
	in   *bufio.Reader
	term *os.File
	out  io.Writer
}

// terminalInput は cmd の入力が端末ならその *os.File を返し、端末でなければ nil を返す。
func terminalInput(cmd *cobra.Command) *os.File {
	if f, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		return f
	}
	return nil
}

func newPrompter(cmd *cobra.Command) *prompter {
	return &prompter{in: bufio.NewReader(cmd.InOrStdin()), term: terminalInput(cmd), out: cmd.ErrOrStderr()}
}

func (p *prompter) line(question string) (string, error) {
	if _, err := fmt.Fprintf(p.out, "%s: ", question); err != nil {
		return "", err
	}
	return p.readLine()
}

// secret は入力が端末なら打った文字を画面に出さずに読み、端末でなければ 1 行として読む。
// 端末で読む間に Ctrl-C を受けたら、端末を元に戻してから同じ SIGINT で終わる。
func (p *prompter) secret(question string) (string, error) {
	if _, err := fmt.Fprintf(p.out, "%s: ", question); err != nil {
		return "", err
	}
	if p.term == nil {
		return p.readLine()
	}
	fd := int(p.term.Fd())
	state, err := term.GetState(fd)
	if err != nil {
		return "", err
	}
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt)
	defer signal.Stop(interrupted)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-interrupted:
			_ = term.Restore(fd, state)
			signal.Reset(os.Interrupt)
			_ = syscall.Kill(syscall.Getpid(), syscall.SIGINT)
		case <-done:
		}
	}()
	password, readErr := term.ReadPassword(fd)
	_, writeErr := fmt.Fprintln(p.out)
	if readErr != nil {
		return "", readErr
	}
	if writeErr != nil {
		return "", writeErr
	}
	return strings.TrimSpace(string(password)), nil
}

// readLine は 1 行を読み、前後の空白を除いて返す。何も読めないまま EOF なら io.ErrUnexpectedEOF を返す。
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

var errNoOptions = errors.New("no options to choose from")

// choose は options を 1 からの番号付きで出し、選ばれた添字を返す。optional なら 0 で notMapped を選べる。
// def が noDefault でなければ空の入力で def を返す。def に notMapped を渡せるのは optional のときだけ。
// optional でなく options が空なら問いを出さずに errNoOptions を返す。範囲外や数字でない入力には選び直しを求める。
func (p *prompter) choose(question string, options []string, def int, optional bool) (int, error) {
	if !optional && len(options) == 0 {
		return 0, errNoOptions
	}
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

	lowest := 1
	if optional {
		lowest = 0
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
		if _, err := fmt.Fprintf(p.out, "enter a number from %d to %d\n", lowest, len(options)); err != nil {
			return 0, err
		}
	}
}
