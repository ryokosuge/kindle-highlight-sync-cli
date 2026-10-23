# AGENTS.md

Kindle の Highlight と Note を Notion Books Database に同期する Go 製 CLI。

## Setup

- Go 1.27 以上（`go.mod` の `go 1.27`。CI は `actions/setup-go` の `go-version-file: go.mod`）
- 依存の取得: `go mod download`
- DB 等の外部サービス・`.env` は不要
- 読む環境変数:
  - `XDG_CONFIG_HOME` / `XDG_DATA_HOME` — config.toml と data ディレクトリの場所
  - `KINDLE_HIGHLIGHT_SYNC_NOTION_TOKEN` — Notion token。無ければ macOS Keychain（service `kindle-highlight-sync` / account `notion-token`）から読む。Linux では Keychain に頼らずこの環境変数を使う
  - `KHS_TEST_CHROME` — テスト専用。Chrome の実行ファイルのパスを入れたときだけ、実 Chrome を起動するテストが走る（空なら skip。CI には Chrome が無い）
- Chrome（ヘッドレス可）は `login` / `sync` の実行時だけ要る。build・テスト・lint には要らない
- `init` / `login` / `sync` を実際に動かすには実 Notion token と Amazon（read.amazon.co.jp）へのログインが要る。手元で確かめられるのは build と `--help` まで

## Build & Test

- build: `go build ./cmd/kindle-highlight-sync`（出力バイナリは `.gitignore` 済み）
- 起動確認: `./kindle-highlight-sync --help`（exit code 0）
- test（全体）: `go test ./...`
- test（1 パッケージ）: `go test ./internal/<pkg>`
- test（1 テスト）: `go test ./cmd/kindle-highlight-sync -run <TestName>`
- test（実 Chrome を含める）: `KHS_TEST_CHROME=<Chrome の実行ファイル> go test ./internal/source`

## Lint

- `golangci-lint run`（CI は v2.14。`.golangci.yml` は `version: "2"` / `linters.default: standard`）
- 手元に無ければ `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0` で入れる（`.devin/blueprint.yaml` と同じ版）。入れたあと `$(go env GOPATH)/bin` が PATH に入っていることを確かめる。入れられない環境では、lint を確かめられなかったことを PR 本文に書く

## Conventions

ディレクトリ:

- `cmd/kindle-highlight-sync/` — `package main` の薄いエントリ層。サブコマンドは `init` / `login` / `sync`
  - `main.go` に `main` / `run` / `execute` / `report`、`root.go` に cobra の root と `PersistentPreRunE`（config.toml 読込）
  - `init.go`（`init --yes` は対話しない）/ `login.go` / `sync.go` が各サブコマンド、`prompt.go` が対話入力
  - `browser.go` の `openBrowser` は変数にしてあり、テストで差し替える
- `internal/config` — config.toml の読込（go-toml/v2、未知 key は拒否）と XDG パス解決。エラー文言に値を含めず path / key / 位置だけを書く
- `internal/destination` — 同期先の抽象（§6.2）と Book の照合（§9）。Notion への書き込みの失敗は `exitcode.Notion`(4)、ローカル状態の不整合は `LocalState`(6)
- `internal/domain` — Book / Highlight の正準モデル。`NewBookID` は ASIN があればそのまま、無ければ §14 の正規化（NFC → 前後空白除去 → CRLF→LF → 水平空白の畳み込み）をした `v1` スキームの title + author の sha256 先頭 32 桁に `b1-` を付ける
- `internal/exitcode` — `Kind`（1〜9）と `*Error`。0 成功 / 1 一般 / 2 Amazon 認証 / 3 設定 / 4 Notion / 5 ブラウザ / 6 ローカル状態 / 7 ソース形式 / 8 一部失敗 / 9 実行中。種類の無いエラーは General(1)。判定は `errors.As`
- `internal/lock` — gofrs/flock による `run.lock` の非待機排他。競合は `exitcode.AlreadyRunning`(9)
- `internal/notion` — Notion API client。`New(token, opts...)` と `WithBaseURL`。retry は最大 5 回の指数バックオフ。`ParseDatabaseID` は URL / 32 桁 hex / UUID から ID を取る。エラーは `exitcode.Notion`(4)
- `internal/redact` — stderr に出す前に secret / Authorization / Cookie / Notion token を `[REDACTED]` に置換する
- `internal/secrets` — Notion token を環境変数 → Keychain の順で探す。保存は `SaveNotionToken`。エラー文言に token を含めない
- `internal/source` — chromedp で Chrome を起動し（`Open` / `Close`）、Kindle Notebook を開いて goquery で解析する
  - `login` と `sync` は同じ `ProfileDir`（`paths.BrowserProfileDir()`）を渡す。`ExecPath` は空にして chromedp の既定の場所に探させる
  - セレクタは定数にする。ページ取得は `fetchPage` / `fetchHighlightsPage` 型にしてテストで差し替える
  - 構造の異常は `newFormatError(what)` で `SourceFormat`(7) にする。`what` に HTML 由来の値を入れない
  - 認証状態は `AuthState`（Unknown / OK / Expired）。DOM が想定と違うときは Unknown にし、Expired と断定しない
- `internal/state` — state.db（sqlite）に同期の状態を保存する StateStore（§6.3）。Initial Sync の判定（§47）は `sync_meta` の `initial_sync_completed_at` の有無だけで行う。エラーは `exitcode.LocalState`(6)
- `internal/sync` — Source と Destination をつなぐ Sync Engine（§4）。skip か失敗が 1 件でもあれば `ApplyResult.PartialFailure` が `exitcode.PartialFailure`(8) を返す

エラー処理と出力:

- panic は `execute` で recover して General(1) にする
- cobra は `SilenceUsage` / `SilenceErrors`。エラー出力は `report` に一元化し、必ず `redact` を通す
- help / completion / `__complete` は設定読込をスキップする

テスト:

- 対象と同じディレクトリに `<file>_test.go` を置き、同一 package の内部テストにする
- 標準 `testing` のみ（testify 等は使わない）。テーブル駆動 `[]struct{name, ...}` + `t.Run`、ケース名は日本語、メッセージは `"<調べた式> = %v, want %v"` の形（例: `line() = %q, want %q`、`stderr = %q, want %q`）、ヘルパーは `t.Helper()`
- 実ホームを触らない。`t.TempDir()` と `t.Setenv`（`XDG_CONFIG_HOME` / `XDG_DATA_HOME`）で隔離する（`root_test.go` の `setupXDG`）
- CLI は `run(args, &stdout, &stderr)` を直接呼び、exit code と stderr を検証する。`openBrowser` / `syncBody` はテストで差し替えて `t.Cleanup` で戻す
- 外部 I/O はテストダブルで固定する: Notion は `httptest.NewServer` + `notion.WithBaseURL`、Keychain は `keyring.MockInit()`、Chrome は偽 CDP プロセス
- `httptest.NewServer` の handler の中では `t.Fatalf` / `t.FailNow` を使わない（handler はテストの goroutine の外で動くため）。handler では記録だけして `t.Errorf` を使うか、要求を記録してテスト本体で検証する
- `internal/source` の HTML fixture は `internal/source/testdata/` に `<種別>_<状態>.html`（`auth_*` / `library_*` / `highlights_*`）で置く。それ以外の fixture は文字列定数かテスト内で生成する
- 別プロセスが要るテストは `os/exec` + 環境変数（例: `KHS_LOCK_HELPER_PATH`）で起動する
- 外部ネットワーク・実 Amazon / 実 Notion に触れるテストは書かない

コメントは日本語、公開識別子は英語。仕様書の節は `§<番号>` で参照する。

<!--
根拠: go.mod, .github/workflows/ci.yml, .golangci.yml, README.md, .gitignore,
cmd/kindle-highlight-sync/{main,root,init,login,sync,browser,prompt}.go と *_test.go,
internal/exitcode/exitcode.go, internal/lock/lock_test.go, internal/secrets/secrets.go,
internal/source/browser_test.go, internal/source/auth_test.go, internal/source/testdata/,
internal/notion, internal/domain
setup（go mod download）と 1 パッケージ / 1 テストの実行コマンドは repo に明記が無く、Go の標準コマンドから導出した
lint の入れ方: .devin/blueprint.yaml の initialize（golangci-lint@v2.14.0）から写した
handler 内の t.Fatalf の禁止: Go の testing の制約（FailNow はテストの goroutine から呼ぶ）
-->
