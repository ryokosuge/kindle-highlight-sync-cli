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

## sync

`./kindle-highlight-sync sync` は Kindle Notebook の Book と Highlight を取得し、Notion Books Database と照合する。`--headed` は画面付きの Chrome で動かす。

`state.db` に `initial_sync_completed_at` が無い実行は Initial Sync として扱い、Notion の既存ページと照合した予定を組んでから、Notion に新しく作る Book（Create）と既存ページに結び付ける Book（Adopt）の件数を示して `Continue? [y/N]` で確認を求める。既定は No で、`y` 以外の答えは exit 3 になる。入力が端末でないときは問いを出さずに exit 3 で止まるので、cron や launchd から動かすときは `--yes` を付ける。`--yes` は Initial Sync の確認を省いて進める。Create と Adopt がどちらも 0 件なら確認は出ない。

確認を通過した実行が成功、Book 単位の失敗（exit 7）、または ambiguous の skip や書き込みの失敗（exit 8）で終わると `state.db` に `initial_sync_completed_at` を記録し、次回からは Initial Sync の予定の算出も確認も行わない。記録はブラウザを閉じ終えた後に行い、確認を拒否した実行、設定が足りない実行（exit 3）、Amazon の認証が切れた実行（exit 2）、ブラウザを閉じるのに失敗した実行（exit 5）では記録しない。記録に失敗すると exit 6 になる。

`--dry-run` は Notion と State を変えずに Book の照合の結果と追記する Highlight の数だけを出す。確認も記録もせず、`state.db` も作らない。
