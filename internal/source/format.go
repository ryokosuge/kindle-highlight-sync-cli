package source

import (
	"errors"
	"fmt"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
)

// ErrSourceFormat は Notebook の構造が想定と違うことを表す（仕様書 §55）。
var ErrSourceFormat = errors.New("unexpected notebook structure")

// newFormatError は what（見つからなかった構造を表す固定の語句）を添えて、
// SourceFormat（exit 7）の種類で包んだエラーを返す。what に HTML から取り出した値を入れない。
// 構造の異常を種類で包むのはここだけで、呼び出し側は fmt.Errorf("...: %w", err) で文脈を足すだけにする。
func newFormatError(what string) error {
	return exitcode.New(exitcode.SourceFormat, fmt.Errorf("%s: %w", what, ErrSourceFormat))
}

// wrapFormatError は原因の err を %w で残したまま SourceFormat（exit 7）の種類で包む。
// 行 id から Highlight ID が作れない（domain.ErrMissingAnnotationID）ように、原因の種類も errors.Is で辿らせたいときに使う。
// 文言は err の文言に ErrSourceFormat を続けた形で、newFormatError と同じく HTML から取り出した値を足さない。
func wrapFormatError(err error) error {
	return exitcode.New(exitcode.SourceFormat, fmt.Errorf("%w: %w", err, ErrSourceFormat))
}
