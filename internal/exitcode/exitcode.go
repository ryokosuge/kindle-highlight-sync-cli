// Package exitcode は失敗の種類と仕様書 §71 の exit code の対応を定める。
package exitcode

import (
	"errors"
	"strings"
)

// Kind は失敗の種類を表す。
type Kind int

// §71 の 1〜9 に対応する。値がそのまま exit code になる。
const (
	General        Kind = 1
	AmazonAuth     Kind = 2
	Config         Kind = 3
	Notion         Kind = 4
	Browser        Kind = 5
	LocalState     Kind = 6
	SourceFormat   Kind = 7
	PartialFailure Kind = 8
	AlreadyRunning Kind = 9
)

var kindNames = map[Kind]string{
	General:        "general error",
	AmazonAuth:     "Amazon authentication required",
	Config:         "configuration / confirmation required",
	Notion:         "Notion error",
	Browser:        "browser error",
	LocalState:     "local state / ID scheme error",
	SourceFormat:   "source format error",
	PartialFailure: "partial failure",
	AlreadyRunning: "already running",
}

// String は Kind に対応する仕様書 §71 の名前を返す。
func (k Kind) String() string {
	return kindNames[k]
}

// Error は失敗の種類と元のエラーを保持する。
type Error struct {
	Kind Kind
	Err  error
}

// Error は元のエラー文字列を返す。
func (e *Error) Error() string {
	return e.Err.Error()
}

// Unwrap は元のエラーを返す。
func (e *Error) Unwrap() error {
	return e.Err
}

// Wrap はエラーに失敗の種類を付与する。
func Wrap(kind Kind, err error) error {
	return &Error{Kind: kind, Err: err}
}

func kindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return General
}

// Code はエラーに対応する exit code を返す。
func Code(err error) int {
	if err == nil {
		return 0
	}
	return int(kindOf(err))
}

// Line は種類とエラーを改行のない一行にして返す。
func Line(err error) string {
	msg := err.Error()
	msg = strings.Join(strings.FieldsFunc(msg, func(r rune) bool {
		return r == '\n' || r == '\r'
	}), " ")
	return kindOf(err).String() + ": " + msg
}
