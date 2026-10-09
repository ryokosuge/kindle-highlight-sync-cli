package domain

// Highlight は Book の中で引いた線 1 つと、それに付いた Note の組（仕様書 §5）。
// Note だけのもの・本文が表示されないものは Text が空になる（§19）。
type Highlight struct {
	ID       string // Highlight ID（a- で始まる値。§16）
	BookID   string // Book.ID
	Text     string
	Note     string
	Location string
	Page     string
}
