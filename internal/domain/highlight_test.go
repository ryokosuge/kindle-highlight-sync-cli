package domain

import "testing"

func TestHighlight(t *testing.T) {
	h := Highlight{
		ID:       "a-QWERTY123",
		BookID:   "B000123456",
		Text:     "引いた線の本文",
		Note:     "自分のメモ",
		Location: "123",
		Page:     "45",
	}
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "ID", got: h.ID, want: "a-QWERTY123"},
		{name: "BookID", got: h.BookID, want: "B000123456"},
		{name: "Text", got: h.Text, want: "引いた線の本文"},
		{name: "Note", got: h.Note, want: "自分のメモ"},
		{name: "Location", got: h.Location, want: "123"},
		{name: "Page", got: h.Page, want: "45"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("Highlight.%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}
