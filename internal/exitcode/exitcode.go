// Package exitcode は失敗の種類と仕様書 §71 の exit code の対応を定める。
package exitcode

import (
	"errors"
	"strings"
)

// Kind は失敗の種類を表す。
type Kind int

const (
	// General は一般的なエラーを表す。
	General Kind = 1
	// AmazonAuth は Amazon 認証が必要なエラーを表す。
	AmazonAuth Kind = 2
	// Config は設定または確認が必要なエラーを表す。
	Config Kind = 3
	// Notion は Notion エラーを表す。
	Notion Kind = 4
	// Browser はブラウザーエラーを表す。
	Browser Kind = 5
	// LocalState はローカル状態または ID 方式のエラーを表す。
	LocalState Kind = 6
	// SourceFormat はソース形式のエラーを表す。
	SourceFormat Kind = 7
	// PartialFailure は一部失敗を表す。
	PartialFailure Kind = 8
	// AlreadyRunning はすでに実行中であることを表す。
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
