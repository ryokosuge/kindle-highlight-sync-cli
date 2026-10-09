# kindle-highlight-sync

Kindle の Highlight と Note を Notion Books Database に同期する CLI。

## macOS での build と起動

1. Go 1.27 以上を用意する。https://go.dev/dl/ の macOS 向けインストーラ、または `brew install go` でインストールできる。`go version` で確認する。
2. `git clone https://github.com/ryokosuge/kindle-highlight-sync-cli.git` を実行し、`cd kindle-highlight-sync-cli` でディレクトリに移動する。
3. `go build ./cmd/kindle-highlight-sync` でバイナリを build する。
4. `./kindle-highlight-sync --help` を実行する。説明 1 行が表示される。`echo $?` で `0` を確認する。

## init

`./kindle-highlight-sync init` は、Notion token、Books Database、Data Source、Property の対応を順に確認し、`$XDG_CONFIG_HOME/kindle-highlight-sync/config.toml`（未設定時は `~/.config/kindle-highlight-sync/config.toml`）を作成する。

token は `KINDLE_HIGHLIGHT_SYNC_NOTION_TOKEN` が設定されていればその実行だけで使い、`config.toml` には保存しない。未設定なら入力を求め、macOS Keychain の service `kindle-highlight-sync`、account `notion-token` に保存する。`--database-id` で Database ID を指定できる。`--yes` は対話せず既定値だけで進めるため、token が見つからない場合、Data Source が複数ある場合、title または kindle_id の既定 Property がない場合は exit 3 になる。

`--force` は `config.toml` だけを作り直し、`browser-profile` と `state.db` は保持する。exit 3 は設定または確認が必要な場合、exit 4 は Notion API のエラーを表す。
