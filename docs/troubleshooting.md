# つまずいたとき

`kindle-highlight-sync` が 0 以外の exit code で終わったときの、原因の見分け方と次に打つ手を exit code ごとにまとめます。値と名前の一覧は [README の「exit code」の節](../README.md#exit-code)を参照してください。

### exit 2: Amazon の認証が切れている

`sync` の実行中に Amazon のログイン状態（`browser-profile`）が切れているとこの値で止まります。stderr には `Amazon authentication required:` で始まり `kindle-highlight-sync login` を案内する文言が出ます。

`kindle-highlight-sync login` を実行して、開いた Chrome で Amazon にログインし直してください。そのあとで `kindle-highlight-sync sync` をもう一度実行します。

`login` 自身が exit 2 で終わるときは、10 分以内に Kindle Notebook まで進めなかったときです。もう一度 `kindle-highlight-sync login` を実行してください。

### exit 3: 設定または確認が必要

stderr の文言で原因を見分けます。

- `config.toml` が無い。stderr に `not found` と `init` を案内する文言が出ます。`kindle-highlight-sync init` を実行してください。
- `config.toml` はあるが Notion token が見つからない。stderr に `notion token not found` と `init` を案内する文言が出ます。このとき `kindle-highlight-sync init` をそのまま実行すると `already exists` と `init --force` を案内して止まるので、次のどちらかで token を渡してください。
  - `kindle-highlight-sync init --force` を実行して token を入力し、Keychain に保存します。`config.toml` だけを作り直し、`browser-profile` と `state.db` はそのまま残ります。
  - 環境変数 `KINDLE_HIGHLIGHT_SYNC_NOTION_TOKEN` に token を設定して `kindle-highlight-sync sync` を実行します。
- `config.toml` が壊れている。`kindle-highlight-sync init --force` で作り直してください。
- `init` の実行中に Books Database を読めなかった。integration が接続されていないか、Database の URL / ID が違うときで、stderr に接続の手順が出ます。[notion-setup](notion-setup.md) の手順どおりに integration を接続してから、もう一度 `kindle-highlight-sync init` を実行してください。
- Initial Sync の確認が通っていない。入力が端末でないとき（cron や launchd からの実行）は stderr に `--yes` の案内が出ます。端末では `y` か `yes` だけが確認として通り、それ以外を答えたときも同じ exit 3 です。内容を確かめて進めるなら `kindle-highlight-sync sync` を端末で動かして `y` と答えるか、`kindle-highlight-sync sync --yes` を実行してください。

### exit 4: Notion に繋がらない、または権限が足りない

`sync` の途中で Notion が認証・権限・対象なし（401 / 403 / 404）を返したときに止まります。原因は token の失効、integration の Books Database への接続が外れた、権限不足、Database の ID 違いのどれかです。[notion-setup](notion-setup.md) の接続の手順のところで、Books Database の「接続」に integration が残っているかを確かめてください。

次の手は `kindle-highlight-sync init --force` で `config.toml` と token を作り直すことです。token を差し替えるときも `init --force` を使い、新しい token を入力してください。環境変数 `KINDLE_HIGHLIGHT_SYNC_NOTION_TOKEN` を設定している間はそちらが優先され Keychain には保存されないので、環境変数の値が古くないかも確かめてください。`init --yes` は token を保存しません。

### exit 5: Chrome を起動できない

`login` と `sync` が Chrome を起動できなかったときに止まります。Chrome の場所を指定する環境変数・フラグ・設定はなく、chromedp の既定の場所から探します。macOS では `/Applications/Google Chrome.app`（または `/Applications/Chromium.app`）にインストールしてから、もう一度実行してください。

ブラウザを閉じるのに失敗したときも exit 5 になります。その場合は残っている Chrome のプロセスを終了してから、もう一度実行してください。

### exit 9: 別の実行が動いている

別の `sync` か `login` が実行中のときに起きます。launchd や cron の定期実行と、手で打った実行が重なる場合が典型です。実行中の `sync` / `login` が終わるのを待ってから、もう一度実行してください。

排他は `run.lock`（`$XDG_DATA_HOME/kindle-highlight-sync/run.lock`、未設定時は `~/.local/share/kindle-highlight-sync/run.lock`）のロックで行います。プロセスが終わればロックは外れるので、ファイルが残っているだけでは exit 9 にはなりません。手で消す必要はありません。
