package notion

import (
	"strings"
	"testing"
)

const wantID = "1f2e3d4c-5b6a-4978-8695-a4b3c2d1e0f9"
const rawID = "1f2e3d4c5b6a49788695a4b3c2d1e0f9"

func TestParseDatabaseID(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "素の 32 桁", input: rawID},
		{name: "大文字の 32 桁", input: strings.ToUpper(rawID)},
		{name: "ハイフン付き UUID", input: wantID},
		{name: "大文字のハイフン付き UUID", input: strings.ToUpper(wantID)},
		{name: "前後に空白", input: "  " + rawID + "\n"},
		{name: "workspace 付き URL", input: "https://www.notion.so/myworkspace/Kindle-Books-" + rawID},
		{name: "workspace 無し URL", input: "https://www.notion.so/Kindle-Books-" + rawID},
		{name: "タイトル無しで ID だけの path", input: "https://www.notion.so/" + rawID},
		{name: "notion.site の URL", input: "https://myworkspace.notion.site/Kindle-Books-" + rawID},
		{name: "?v= 付き URL", input: "https://www.notion.so/myworkspace/Kindle-Books-" + rawID + "?v=0123456789abcdef0123456789abcdef"},
		{name: "?v= と &pvs= 付き", input: "https://www.notion.so/myworkspace/Kindle-Books-" + rawID + "?v=0123456789abcdef0123456789abcdef&pvs=4"},
		{name: "タイトルにハイフンと数字", input: "https://www.notion.so/myworkspace/2024-Reading-List-12-" + rawID},
		{name: "タイトルが 16 進文字で終わる", input: "https://www.notion.so/myworkspace/Cafe-Bed-" + rawID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDatabaseID(tt.input)
			if err != nil {
				t.Fatalf("ParseDatabaseID() error = %v", err)
			}
			if got != wantID {
				t.Errorf("ParseDatabaseID() = %q, want %q", got, wantID)
			}
		})
	}
}

func TestParseDatabaseID_invalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "空文字", input: ""},
		{name: "空白だけ", input: "   "},
		{name: "31 桁", input: rawID[:31]},
		{name: "33 桁", input: rawID + "0"},
		{name: "16 進以外を含む", input: "g" + rawID[1:]},
		{name: "ハイフンの位置が違う UUID", input: "1f2e3d4c5-b6a-4978-8695-a4b3c2d1e0f9"},
		{name: "ID の無い URL", input: "https://www.notion.so/myworkspace/Kindle-Books"},
		{name: "view id だけを持つ URL", input: "https://www.notion.so/myworkspace/Kindle-Books?v=0123456789abcdef0123456789abcdef"},
		{name: "最後の segment 以外に ID がある URL", input: "https://www.notion.so/" + rawID + "/Kindle-Books"},
		{name: "区切りなしで ID が続く segment", input: "https://www.notion.so/myworkspace/Books" + rawID},
		{name: "scheme の無い URL", input: "www.notion.so/myworkspace/Kindle-Books-" + rawID},
		{name: "ID の後ろに文字が続く", input: "https://www.notion.so/myworkspace/Kindle-Books-" + rawID + "x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDatabaseID(tt.input)
			if err == nil {
				t.Errorf("ParseDatabaseID() error = nil, want error")
			}
			if got != "" {
				t.Errorf("ParseDatabaseID() = %q, want \"\"", got)
			}
		})
	}
}
