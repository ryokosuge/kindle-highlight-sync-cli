// Package lock は run.lock の排他を待たずに取る / 解放する。
package lock

import (
	"errors"

	"github.com/gofrs/flock"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
)

// ErrAlreadyRunning は別のプロセスが排他を持っているときの失敗（仕様書 §39 の文言）。
var ErrAlreadyRunning = errors.New("another kindle-highlight-sync process is running")

// Lock は取得した run.lock の排他を表す。
type Lock struct {
	f *flock.Flock
}

// Acquire は path の排他を待たずに取る。
// 取り合いで取れなければ exitcode.New(exitcode.AlreadyRunning, ErrAlreadyRunning) を返す。
// それ以外の失敗は種類で包まずに返す（§71 の 1）。
func Acquire(path string) (*Lock, error) {
	f := flock.New(path)
	ok, err := f.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, exitcode.New(exitcode.AlreadyRunning, ErrAlreadyRunning)
	}
	return &Lock{f: f}, nil
}

// Release は排他を解放する。run.lock のファイルは消さない。
func (l *Lock) Release() error {
	return l.f.Unlock()
}
