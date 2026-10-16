package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/notion"
)

// setupGuide は init の最初の問いの前に出す、Books Database の準備物の案内。
// 項目の名前・型・必須・用途は notion の Field から組み、固定文言と Field の定義だけを使う。
func setupGuide() string {
	var b strings.Builder
	b.WriteString("Books Database には次の Property が要ります（Notion の型名はそのまま書いています）。\n")
	for _, f := range notion.Fields() {
		label := f.DefaultName()
		if label == "" {
			label = f.Type() + " 型の Property（名前は任意）"
		}
		need := "任意"
		if f.Required() {
			need = "必須"
		}
		fmt.Fprintf(&b, "  - %s [%s・%s]: %s\n", label, f.Type(), need, f.Purpose())
	}
	b.WriteString("作った integration を Books Database に接続してください（Database の右上の「…」メニューから「接続」で追加します）。接続していないと Database を読めません。\n\n")
	return b.String()
}

func printSetupGuide(w io.Writer) {
	_, _ = io.WriteString(w, setupGuide())
}
