package destination

import "testing"

func TestSyncMarkerID(t *testing.T) {
	tests := []struct {
		name, text, wantID string
		wantOK             bool
	}{
		// 1 つ
		{
			name:   "本文の後の Sync Marker を取り出す",
			text:   "本文\n位置 12 · khs:a-abc123",
			wantID: "a-abc123",
			wantOK: true,
		},
		{
			name:   "大文字や数字やアンダースコアを含む ID を取り出す",
			text:   "本文\n位置 1 · khs:a-AB_9-z",
			wantID: "a-AB_9-z",
			wantOK: true,
		},
		// 複数
		{
			name:   "最後の一致を取り出す",
			text:   "khs:a-fake を本文に書いた\n位置 3 · khs:a-real",
			wantID: "a-real",
			wantOK: true,
		},
		{
			name:   "後の khs: に許可文字が無ければ前の一致を取り出す",
			text:   "本文 · khs:a-first\nkhs:",
			wantID: "a-first",
			wantOK: true,
		},
		// 区切り
		{
			name:   "末尾の改行で終わる",
			text:   "· khs:h1-abc\n",
			wantID: "h1-abc",
			wantOK: true,
		},
		{
			name:   "空白と続きの文字があっても取り出す",
			text:   "· khs:h1-abc 続き",
			wantID: "h1-abc",
			wantOK: true,
		},
		{
			name:   "句点を区切りとして取り出す",
			text:   "· khs:h1-abc。",
			wantID: "h1-abc",
			wantOK: true,
		},
		{
			name:   "カンマを区切りとして取り出す",
			text:   "· khs:h1-abc,",
			wantID: "h1-abc",
			wantOK: true,
		},
		{
			name:   "ピリオドを区切りとして取り出す",
			text:   "· khs:h1-abc.def",
			wantID: "h1-abc",
			wantOK: true,
		},
		// 無し
		{
			name:   "空文字列なら一致しない",
			text:   "",
			wantID: "",
			wantOK: false,
		},
		{
			name:   "本文に Sync Marker が無ければ一致しない",
			text:   "本文だけ\n位置 12",
			wantID: "",
			wantOK: false,
		},
		{
			name:   "ID の無い Sync Marker は一致しない",
			text:   "· khs:",
			wantID: "",
			wantOK: false,
		},
		{
			name:   "コロンの直後に空白があれば一致しない",
			text:   "· khs: a-abc",
			wantID: "",
			wantOK: false,
		},
		{
			name:   "コロンの直後が句点なら一致しない",
			text:   "· khs:。",
			wantID: "",
			wantOK: false,
		},
		// prefix の違い
		{
			name:   "a- prefix の ID を取り出す",
			text:   "· khs:a-abc",
			wantID: "a-abc",
			wantOK: true,
		},
		{
			name:   "h1- prefix の ID を取り出す",
			text:   "· khs:h1-abc",
			wantID: "h1-abc",
			wantOK: true,
		},
		{
			name:   "b1- prefix の ID を取り出す",
			text:   "· khs:b1-0123",
			wantID: "b1-0123",
			wantOK: true,
		},
		{
			name:   "prefix のない ID も取り出す",
			text:   "· khs:xyz",
			wantID: "xyz",
			wantOK: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if id, ok := SyncMarkerID(tt.text); id != tt.wantID || ok != tt.wantOK {
				t.Errorf("SyncMarkerID(%q) = (%q, %v), want (%q, %v)", tt.text, id, ok, tt.wantID, tt.wantOK)
			}
		})
	}
}
