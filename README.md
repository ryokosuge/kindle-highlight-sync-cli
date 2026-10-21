# kindle-highlight-sync

Kindle の Highlight と Note を Notion Books Database に同期する CLI。

Amazon.co.jp の Kindle Notebook から Book ごとの Highlight と Note を取り出し、Notion の Books Database に同期する。

動く環境:

- macOS（対象は macOS だけで、Linux / Windows は対象外。動作確認と配布をしない）
- Google Chrome（`login` と `sync` の実行時に使う）
- Notion integration（その token が要る）

ライセンスは MIT。詳細は [LICENSE](LICENSE) を参照する。

## インストール

1. Go 1.27 以上を用意する。https://go.dev/dl/ の macOS 向けインストーラ、または `brew install go` でインストールできる。`go version` で確認する。
2. `git clone https://github.com/ryokosuge/kindle-highlight-sync-cli.git` を実行し、`cd kindle-highlight-sync-cli` でディレクトリに移動する。
3. `go build ./cmd/kindle-highlight-sync` でバイナリを build する。
4. `./kindle-highlight-sync --help` を実行する。説明 1 行が表示される。`echo $?` で `0` を確認する。

以降の手順は、build したカレントディレクトリのバイナリを `./kindle-highlight-sync` として呼ぶ。

## Notion の準備

`init` の前に、Notion 側で次の 3 つを用意する。`init` も最初の問いの前に同じ内容を案内する。

1. integration を作って token を取る。workspace の owner が integration を作り、token を取得する。作り方は公式の https://developers.notion.com/docs/authorization を参照する。
2. Books Database に次の Property を用意する（Notion の型名はそのまま書く）。

   | Property | 型 | 必須 | 内容 |
   |---|---|---|---|
   | （名前は任意） | title | 必須 | Book のタイトル |
   | Kindle ID | rich_text | 必須 | Book ID（ASIN、取れなければ `b1-` で始まる代替値）。同期先のページを見分けるのに使う |
   | Author | rich_text | 任意 | Book の著者 |
   | ASIN | rich_text | 任意 | Book の ASIN |
   | Last Annotated At | date | 任意 | Kindle Notebook で最後に Highlight や Note を付けた日時 |

3. 作った integration を Books Database に接続する。Notion で Books Database のページを開き、右上の「…」メニューから「接続」（英語表示では Connections / Add connections）で integration を追加する。接続していないと Database を読めない。

token の渡し方は 2 通りで、`config.toml` には token を書かない。

- 環境変数 `KINDLE_HIGHLIGHT_SYNC_NOTION_TOKEN` に token を設定する。設定されていればその実行だけで使う。
- 環境変数を設定しない場合は、`init` が入力を求め、入力した token を macOS Keychain の service `kindle-highlight-sync`、account `notion-token` に保存する。

## login

`./kindle-highlight-sync login` は、画面付きの Chrome を開く。「ブラウザで Amazon にログインしてください」と 1 行出るので、開いた Chrome で Amazon にログインする（MFA の入力も同じ画面で行う）。Kindle Notebook まで進むとコマンドは exit 0 で終わる。終了時に成功の文言は出ない。10 分以内に Kindle Notebook に届かないときは exit 2 になる。

ログインの状態は `browser-profile`（`$XDG_DATA_HOME/kindle-highlight-sync/browser-profile`、未設定時は `~/.local/share/kindle-highlight-sync/browser-profile`）に保存され、`sync` がそのまま使う。exit 2 などで止まったときは「つまずいたとき」を参照する。

## init

`./kindle-highlight-sync init` は、Notion token、Books Database、Data Source、Property の対応を順に確認し、`$XDG_CONFIG_HOME/kindle-highlight-sync/config.toml`（未設定時は `~/.config/kindle-highlight-sync/config.toml`）を作成する。token の用意は「Notion の準備」を参照する。うまくいくと `saved <config.toml のパス>` と `database_id:` などの設定内容が表示される。

`--database-id` で Database ID を指定できる。`--yes` は対話せず既定値だけで進めるため、token が見つからない場合、Data Source が複数ある場合、title または kindle_id の既定 Property がない場合は exit 3 になる。

`--force` は `config.toml` だけを作り直し、`browser-profile` と `state.db` は保持する。exit 3 は設定または確認が必要な場合、exit 4 は Notion API のエラーを表す。つまずいたときは「つまずいたとき」を参照する。

## sync

`./kindle-highlight-sync sync` は Kindle Notebook の Book と Highlight を取得し、Notion Books Database と照合する。`--headed` は画面付きの Chrome で動かす。

`state.db` に `initial_sync_completed_at` が無い実行は Initial Sync として扱い、Notion の既存ページと照合した予定を組んでから、Notion に新しく作る Book（Create）と既存ページに結び付ける Book（Adopt）の件数を示して `Continue? [y/N]` で確認を求める。既定は No で、`y` 以外の答えは exit 3 になる。入力が端末でないときは問いを出さずに exit 3 で止まるので、cron や launchd から動かすときは `--yes` を付ける。`--yes` は Initial Sync の確認を省いて進める。Create と Adopt がどちらも 0 件なら確認は出ない。

確認を通過した実行が成功するか、Book 単位の失敗（exit 8）で終わると `state.db` に `initial_sync_completed_at` を記録し、次回からは Initial Sync の予定の算出も確認も行わない。exit 8 になる Book 単位の失敗は、ASIN の無い Book、その Book の Highlight ページだけの構造の異常、Highlight ID が作れない Book、ambiguous の skip、1 冊の書き込みの失敗（400 / 429 / 5xx / タイムアウトなど）で、失敗した Book 以外は同期する。記録はブラウザを閉じ終えた後に行い、確認の前に止まる実行（Notebook 画面全体の構造の異常による exit 7 など）、確認を拒否した実行、設定が足りない実行（exit 3）、Amazon の認証が切れた実行（exit 2）、ブラウザを閉じるのに失敗した実行（exit 5）では記録しない。記録に失敗すると exit 6 になる。exit 2 / 3 / 4 / 5 / 9 で止まったときは「つまずいたとき」を参照する。

通常の `sync` が対象にするのは、`state.db` の `books` に記録が無い Book、Kindle の Last Annotated Date が無い Book、および Last Annotated Date が記録済みの `last_synced_at` の UTC の暦日の前日以降にある Book だけで、`last_synced_at` が Last Annotated Date より 2 日以上後の Book は Highlight の取得も Notion への要求も行わない。ASIN の無い Book は同期を終えられず記録が付かないので、常に対象に残る。`--full` を付けると `state.db` の記録に関わらず全 Book を対象にする。標準出力の末尾に `Target books  <対象の数> of <全 Book の数>` の行が出る。

`--dry-run` は Notion と State を変えずに Book の照合の結果と追記する Highlight の数だけを出す。確認も記録もせず、`state.db` も作らない（対象の選択のために `state.db` の `books` は読むが、無ければ作らず、あっても内容を変えない）。`--full` と併用すると全 Book の予定を出す。

## exit code

0 は成功。失敗したときは stderr の先頭に次の表の名前が出て、対応する値で終了する。対話式の `init` だけは、案内とプロンプトを先に stderr に出すため、名前はそれらの後に出る。

| 値 | 名前 | 意味 |
|---|---|---|
| 1 | general error | 上のどれにも当たらない失敗。`sync` が Notebook に届かず、認証切れとも判定されないときも含む |
| 2 | Amazon authentication required | Amazon の認証が切れている。`login` が 10 分以内に Notebook に届かないときも含む |
| 3 | configuration / confirmation required | 設定または確認が必要。Notion token が見つからない、`init` 時に integration が Books Database に接続されていない、Initial Sync の確認に `y` 以外を答えた（または端末でない）ときなど |
| 4 | Notion error | `sync` 中の Notion の失敗のうち、認証・権限・対象が無い（401 / 403 / 404）など、残りの Book を書かずに止まるもの。1 冊の書き込みの失敗（400 / 429 / 5xx / タイムアウトなど）は exit 8 |
| 5 | browser error | Chrome の起動または終了の失敗 |
| 6 | local state / ID scheme error | `state.db` の読み書きの失敗、または ID の方式が合わない |
| 7 | source format error | Kindle Notebook 画面全体の構造が想定と違う |
| 8 | partial failure | Book 単位の失敗。1 冊の書き込みの失敗（400 / 429 / 5xx / タイムアウトなど）、その Book の Highlight ページだけの構造の異常、Highlight ID が作れない Book、ASIN の無い Book、照合が ambiguous で skip した Book。失敗した Book 以外は同期する。Notebook 画面全体の構造の異常は exit 7 |
| 9 | already running | 別の `kindle-highlight-sync` が実行中 |

exit 2 / 3 / 4 / 5 / 9 の原因と次の手は「つまずいたとき」を参照する。

## つまずいたとき

exit code ごとの原因と次の手。値と名前の一覧は「exit code」の表を参照する。

### exit 2: Amazon の認証が切れている

`sync` の stderr に `run` で始まる `login` の案内が出る。Amazon のログイン状態（`browser-profile`）が切れているのが原因。`./kindle-highlight-sync login` を実行し、開いた Chrome で Amazon にログインし直してから `sync` をもう一度実行する。`login` 自身が exit 2 になるときは、10 分以内に Kindle Notebook に届かなかった。もう一度 `login` を実行する。

### exit 3: 設定または確認が必要

stderr の文言で原因を見分ける。

- `config.toml` が無い、または Notion token が見つからない。stderr に `init` を案内する文言が出る。`./kindle-highlight-sync init` を実行する。token は環境変数 `KINDLE_HIGHLIGHT_SYNC_NOTION_TOKEN` か Keychain から読むので、「Notion の準備」の渡し方のどちらかを済ませる。`config.toml` が既にあるのに壊れているときも exit 3 になる。`./kindle-highlight-sync init --force` で作り直す。
- `init` 時に integration が Books Database に接続されていない。stderr に日本語の接続手順が出る。「Notion の準備」の手順 3 のとおり接続してから、もう一度 `init` を実行する。
- Initial Sync の確認が通っていない。端末でない入力（cron や launchd など）で動かしたときは stderr に `--yes` の案内が出る。端末で `y` 以外を答えたときも同じ exit 3 になる。内容を確かめて進めるなら、`./kindle-highlight-sync sync` を端末で動かして `y` を答えるか、`./kindle-highlight-sync sync --yes` を実行する。

### exit 4: Notion に繋がらない、または権限が足りない

`sync` 中に Notion が認証・権限・対象なし（401 / 403 / 404）を返したときに止まる。原因は、token の失効、integration の Books Database への接続が外れた、権限不足、Database の ID 違いのどれか。確かめる場所は Notion の Books Database の「接続」の設定（「Notion の準備」の手順 3）で、integration が追加されているかを見る。

次の手は `./kindle-highlight-sync init --force` で `config.toml` と token を作り直すこと。token を差し替えるときも `init --force` を使い、新しい token を入力する。環境変数 `KINDLE_HIGHLIGHT_SYNC_NOTION_TOKEN` を設定している間は、そちらが優先され Keychain には保存されない。環境変数の値が古くないかも確かめる。`init --yes` は token を保存しない。

### exit 5: Chrome を起動できない

`login` と `sync` が Chrome を起動できなかった。Chrome の場所を指定する環境変数・フラグ・設定は無く、chromedp の既定の場所から探す。macOS では `/Applications/Google Chrome.app`（または `/Applications/Chromium.app`）にインストールされている必要がある。`/Applications` に Google Chrome を入れてからもう一度実行する。ブラウザを閉じるのに失敗したときも exit 5 になる。その場合は残っている Chrome のプロセスを終了して、もう一度実行する。

### exit 9: 別の実行が動いている

別の `sync` か `login` が実行中のときに起きる。launchd や cron の定期実行と、手で打った実行が重なる場合が典型。実行中の `sync` / `login` が終わるのを待ってから、もう一度実行する。排他は `run.lock`（`$XDG_DATA_HOME/kindle-highlight-sync/run.lock`、未設定時は `~/.local/share/kindle-highlight-sync/run.lock`）のロックで行い、プロセスが終わればロックは外れる。ファイルが残っているだけでは exit 9 にならず、手で消す必要は無い。
