# kindle-highlight-sync

Kindle の Highlight と Note を Notion Books Database に同期する CLI。

Amazon.co.jp の Kindle Notebook から Book ごとの Highlight と Note を取り出し、Notion の Books Database に同期する。

動く環境:

- macOS（対象は macOS だけで、Linux / Windows は対象外。動作確認と配布をしない）
- Google Chrome（`login` と `sync` の実行時に使う）
- Notion integration（その token が要る）

ライセンスは MIT。詳細は [LICENSE](LICENSE) を参照する。

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

確認を通過した実行が成功するか、Book 単位の失敗（exit 8）で終わると `state.db` に `initial_sync_completed_at` を記録し、次回からは Initial Sync の予定の算出も確認も行わない。exit 8 になる Book 単位の失敗は、ASIN の無い Book、Highlight の取得の失敗、ambiguous の skip、書き込みの失敗で、失敗した Book 以外は同期する。記録はブラウザを閉じ終えた後に行い、確認の前に止まる実行（Notebook 画面全体の構造の異常による exit 7 など）、確認を拒否した実行、設定が足りない実行（exit 3）、Amazon の認証が切れた実行（exit 2）、ブラウザを閉じるのに失敗した実行（exit 5）では記録しない。記録に失敗すると exit 6 になる。

通常の `sync` が対象にするのは、`state.db` の `books` に記録が無い Book、Kindle の Last Annotated Date が無い Book、および Last Annotated Date が記録済みの `last_synced_at` の UTC の暦日の前日以降にある Book だけで、`last_synced_at` が Last Annotated Date より 2 日以上後の Book は Highlight の取得も Notion への要求も行わない。ASIN の無い Book は同期を終えられず記録が付かないので、常に対象に残る。`--full` を付けると `state.db` の記録に関わらず全 Book を対象にする。標準出力の末尾に `Target books  <対象の数> of <全 Book の数>` の行が出る。

`--dry-run` は Notion と State を変えずに Book の照合の結果と追記する Highlight の数だけを出す。確認も記録もせず、`state.db` も作らない（対象の選択のために `state.db` の `books` は読むが、無ければ作らず、あっても内容を変えない）。`--full` と併用すると全 Book の予定を出す。
