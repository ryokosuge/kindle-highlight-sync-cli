package main

import (
	"fmt"
	"io"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
)

// printConnectGuide は Books Database に integration が接続されていないときの案内を書く。
// 対話の問い（改行なしで終わる）の直後にも出るため、先頭に改行を入れる。固定文言だけを使い、token は混ぜない。
func printConnectGuide(w io.Writer) {
	_, _ = io.WriteString(w, `
Books Database を読めませんでした。integration が Books Database に接続されていないか、Database の URL / ID が違う可能性があります。
接続の手順:
  1. Notion で Books Database のページを開く
  2. 右上の「…」メニューから「接続」（英語表示では Connections / Add connections）を選ぶ
  3. 作った integration を選んで追加する
接続したら、もう一度 init を実行してください。
`)
}

// connectRequiredError は未接続の可能性による失敗を exit 3 の error にする。手順は error 本文に入れず stderr に直接書く。
func connectRequiredError(err error) error {
	return exitcode.New(exitcode.ConfigRequired, fmt.Errorf("integration が接続されていないか、Database の URL / ID が違います: %w", err))
}
