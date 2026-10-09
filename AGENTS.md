# AGENTS.md

Kindle の Highlight と Note を Notion Books Database に同期する Go 製 CLI。

## Setup

- Go 1.27 以上（`go.mod` の `go 1.27`。CI は `actions/setup-go` の `go-version-file: go.mod`）
- 依存の取得: `go mod download`
- 外部サービス・`.env` は不要。読む環境変数は `XDG_CONFIG_HOME` / `XDG_DATA_HOME` のみ

## Build & Test

- build: `go build ./cmd/kindle-highlight-sync`（出力バイナリは `.gitignore` 済み）
- 起動確認: `./kindle-highlight-sync --help`（exit code 0）
- test（全体）: `go test ./...`
- test（1 パッケージ）: `go test ./internal/<pkg>`
- test（1 テスト）: `go test ./cmd/kindle-highlight-sync -run <TestName>`

## Lint

- `golangci-lint run`（CI は v2.14。`.golangci.yml` は `version: "2"` / `linters.default: standard`）

## Conventions

ディレクトリ:

- `cmd/kindle-highlight-sync/` — `package main` の薄いエントリ層。`main.go` に `main` / `run` / `execute` / `report`、`root.go` に cobra の root と `PersistentPreRunE`（config.toml 読込）、`sync.go` に sync サブコマンド
- `internal/config` — config.toml の読込（go-toml/v2、未知 key は拒否）と XDG パス解決。エラー文言に値を含めず path / key / 位置だけを書く
- `internal/exitcode` — `Kind`（1〜9）と `*Error`。0 成功 / 1 一般 / 2 Amazon 認証 / 3 設定 / 4 Notion / 5 ブラウザ / 6 ローカル状態 / 7 ソース形式 / 8 一部失敗 / 9 実行中。種類の無いエラーは General(1)。判定は `errors.As`
- `internal/lock` — gofrs/flock による `run.lock` の非待機排他。競合は `exitcode.AlreadyRunning`(9)
- `internal/redact` — stderr に出す前に secret / Authorization / Cookie / Notion token を `[REDACTED]` に置換する

エラー処理と出力:

- panic は `execute` で recover して General(1) にする
- cobra は `SilenceUsage` / `SilenceErrors`。エラー出力は `report` に一元化し、必ず `redact` を通す
- help / completion は設定読込をスキップする

テスト:

- 対象と同じディレクトリに `<file>_test.go` を置く。`cmd` と `internal/lock` は同一 package の内部テスト
- 標準 `testing` のみ（testify 等は使わない）。テーブル駆動 `[]struct{name, ...}` + `t.Run`、ケース名は日本語、メッセージは `"got = %v, want %v"`、ヘルパーは `t.Helper()`
- 実ホームを触らない。`t.TempDir()` と `t.Setenv`（`XDG_CONFIG_HOME` / `XDG_DATA_HOME`）で隔離する（`root_test.go` の `setupXDG`）
- CLI は `run(args, &stdout, &stderr)` を直接呼び、exit code と stderr を検証する。`syncBody` はテストで差し替えて `t.Cleanup` で戻す
- 別プロセスが要るテストは `os/exec` + 環境変数（例: `KHS_LOCK_HELPER_PATH`）で起動する
- `testdata/` は使っていない。fixture は文字列定数かテスト内で生成する
- 外部ネットワーク・実 Amazon / Notion に触れるテストは書かない（I/O はテストダブルで固定する）

コメントは日本語、公開識別子は英語。仕様書の節は `§<番号>` で参照する。

<!--
根拠: go.mod, .github/workflows/ci.yml, .golangci.yml, README.md, .gitignore,
cmd/kindle-highlight-sync/main.go, cmd/kindle-highlight-sync/root_test.go,
internal/exitcode/exitcode.go, internal/lock/lock_test.go
setup（go mod download）と 1 パッケージ / 1 テストの実行コマンドは repo に明記が無く、Go の標準コマンドから導出した
-->
