# コマンドの使い方

`kindle-highlight-sync` のサブコマンドの使い方とフラグをまとめたページです。インストールは README を参照してください。

## login

`kindle-highlight-sync login` で、画面付きの Chrome を開いて Amazon にログインします。「ブラウザで Amazon にログインしてください」と 1 行出るので、開いた Chrome で Amazon にログインしてください（MFA の入力も同じ画面で行います）。Kindle Notebook まで進むとコマンドは exit 0 で終わります。終了時に成功の文言は出ません。10 分以内に Kindle Notebook に届かないときは exit 2 になります。

```
kindle-highlight-sync login
```

このコマンドにフラグはありません。

ログインの状態は `browser-profile`（`$XDG_DATA_HOME/kindle-highlight-sync/browser-profile`、未設定時は `~/.local/share/kindle-highlight-sync/browser-profile`）に保存され、`sync` がそのまま使います。exit 2 などで止まったときは「[つまずいたとき](troubleshooting.md)」を参照してください。

## init

`kindle-highlight-sync init` で、Notion token、Books Database、Data Source、Property の対応を順に確認し、`$XDG_CONFIG_HOME/kindle-highlight-sync/config.toml`（未設定時は `~/.config/kindle-highlight-sync/config.toml`）を作成します。token の用意は「[Notion の準備](notion-setup.md)」を参照してください。うまくいくと `saved <config.toml のパス>` と `database_id:` などの設定内容が表示されます。

```
kindle-highlight-sync init
```

使えるフラグは次のとおりです。

| フラグ | 意味 |
| --- | --- |
| `--force` | 既存の `config.toml` を作り直す（`browser-profile` と `state.db` は保持する） |
| `--database-id` | Notion Database の ID を指定する（`--yes` では必須） |
| `--yes` | 対話せず既定値で進める |

`--yes` を付けると token が見つからない場合、Data Source が複数ある場合、title または kindle_id の既定 Property がない場合に exit 3 になります。exit 3 は設定または確認が必要な場合、exit 4 は Notion API のエラーを表します。つまずいたときは「[つまずいたとき](troubleshooting.md)」を参照してください。

## sync

`kindle-highlight-sync sync` で、Kindle Notebook の Book と Highlight を取得し、Notion Books Database と照合します。

```
kindle-highlight-sync sync
```

使えるフラグは次のとおりです。

| フラグ | 意味 |
| --- | --- |
| `--headed` | 画面付きの Chrome で同期する（既定は headless） |
| `--dry-run` | Notion と State を変えずに、Book の照合の結果と追記する Highlight の数だけを出す |
| `--full` | State の記録に関わらず全 Book の Highlight を取って同期する（既定は変化のある Book と未同期の Book だけ） |
| `--yes` | Initial Sync の Create / Adopt の確認を省く（入力が端末でないときに要る） |

`state.db` に `initial_sync_completed_at` が無い実行は Initial Sync として扱い、Notion の既存ページと照合した予定を組んでから、Notion に新しく作る Book（Create）と既存ページに結び付ける Book（Adopt）の件数を示して `Continue? [y/N]` で確認を求めます。既定は No で、`y` 以外の答えは exit 3 になります。入力が端末でないときは問いを出さずに exit 3 で止まるので、cron や launchd から動かすときは `--yes` を付けてください。Create と Adopt がどちらも 0 件なら確認は出ません。

確認を通過した実行が成功するか、Book 単位の失敗（exit 8）で終わると `state.db` に `initial_sync_completed_at` を記録し、次回からは Initial Sync の予定の算出も確認も行いません。

通常の `sync` が対象にするのは、`state.db` の `books` に記録が無い Book、Kindle の Last Annotated Date が無い Book、および Last Annotated Date が記録済みの `last_synced_at` の UTC の暦日の前日以降にある Book だけです。`--full` を付けると `state.db` の記録に関わらず全 Book を対象にします。標準出力の末尾に `Target books  <対象の数> of <全 Book の数>` の行が出ます。

`--dry-run` は確認も記録もせず、`state.db` も作りません（対象の選択のために `state.db` の `books` は読みますが、無ければ作らず、あっても内容を変えません）。`--full` と併用すると全 Book の予定を出します。

終了コードの意味は [README の exit code の表](../README.md#exit-code)を参照してください。exit 2 / 3 / 4 / 5 / 9 で止まったときは「[つまずいたとき](troubleshooting.md)」を参照してください。
