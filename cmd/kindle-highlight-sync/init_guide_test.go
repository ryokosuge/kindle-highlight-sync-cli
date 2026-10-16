package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintSetupGuide(t *testing.T) {
	var buf bytes.Buffer
	printSetupGuide(&buf)
	got := buf.String()

	tests := []struct {
		name string
		want string
	}{
		{"title は名前が任意で必須", "title 型の Property（名前は任意） [title・必須]: Book のタイトル\n"},
		{"Kindle ID は rich_text の必須", "  - Kindle ID [rich_text・必須]: Book ID"},
		{"Author は rich_text の任意", "  - Author [rich_text・任意]: Book の著者\n"},
		{"ASIN は rich_text の任意", "  - ASIN [rich_text・任意]: Book の ASIN\n"},
		{"Last Annotated At は date の任意", "  - Last Annotated At [date・任意]: Kindle Notebook で最後に"},
		{"integration の接続の案内", "作った integration を Books Database に接続してください"},
		{"末尾に空行", "を読めません。\n\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if ok := strings.Contains(got, tt.want); !ok {
				t.Errorf("strings.Contains(printSetupGuide(), %q) = %v, want %v (guide = %q)", tt.want, ok, true, got)
			}
		})
	}
	if ok := strings.HasSuffix(got, "\n\n"); !ok {
		t.Errorf("strings.HasSuffix(printSetupGuide(), %q) = %v, want %v (guide = %q)", "\n\n", ok, true, got)
	}
}
