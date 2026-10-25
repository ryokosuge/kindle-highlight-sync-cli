# Notion の準備

`init` を実行する前に、Notion 側で次の 3 つを用意してください。上から順に進められます。

なお、`init` も最初の問いの前に、手順 2 の Property と手順 3 の接続の案内を出します（`--yes` で対話しないときは出ません）。このページのとおりに準備してあれば、そのまま先に進んでください。

## 1. integration を作って token を取る

Notion の Developer portal で integration を作り、API token を取得してください。作れるのは workspace の owner だけです。

Notion はいま integration を connection（接続）と呼び替えつつあり、ワークスペースや画面によって「インテグレーション」「接続」「Connections」など表示名が違うことがあります。このページでは integration と書きますが、画面に connection と出ていたら同じものと読んでください。

手順は公式ドキュメント https://developers.notion.com/docs/authorization を参照してください。Developer portal で新しい connection（integration）を作成し、`Configuration` タブに表示される API token を控えます。

token は秘密の値です。source code や git に書き込まないでください。

## 2. Books Database に Property を用意する

同期先の Books Database に、次の Property を用意してください（Notion の型名はそのまま書いています）。

| Property | 型 | 必須 | 内容 |
|---|---|---|---|
| （名前は任意） | title | 必須 | Book のタイトル |
| Kindle ID | rich_text | 必須 | Book ID（ASIN、取れなければ `b1-` で始まる代替値）。同期先のページを見分けるのに使います |
| Author | rich_text | 任意 | Book の著者 |
| ASIN | rich_text | 任意 | Book の ASIN |
| Last Annotated At | date | 任意 | Kindle Notebook で最後に Highlight や Note を付けた日時 |

title 型の Property は Database に必ず 1 つあるもので、名前は何でも構いません。それ以外は表の名前で作ってください。Author は「著者」、Last Annotated At は「Last Annotated」という名前でも読み取れます。

## 3. 作った integration を Books Database に接続する

1. Notion で Books Database のページを開いてください。
2. 右上の「…」メニューから「接続」（英語表示では Connections / Add connections）を選んでください。
3. 手順 1 で作った integration を探して追加してください。

接続していないと CLI は Database を読めません。`init` の途中で「Books Database を読めませんでした」と出たときも、この手順で接続してから `init` をやり直してください。

## token の渡し方

token の渡し方は 2 通りあります。`config.toml` には token を書かないでください。

- 環境変数 `KINDLE_HIGHLIGHT_SYNC_NOTION_TOKEN` に token を設定してください。設定されていれば CLI はそれを使います。
- 環境変数を設定しない場合、`init` が token の入力を求めます。入力した token は macOS Keychain の service `kindle-highlight-sync`、account `notion-token` に保存されます。

Books Database に Data Source が複数あるときは、`init` がどれを使うかを選ばせる問いを出します（`--yes` のときは選べず exit 3 になります）。
