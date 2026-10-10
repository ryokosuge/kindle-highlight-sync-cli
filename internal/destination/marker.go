package destination

import "regexp"

// syncMarkerPattern は §20 の Sync Marker の抽出正規表現。
var syncMarkerPattern = regexp.MustCompile(`khs:([A-Za-z0-9_-]+)`)

// SyncMarkerID は Quote Block の joinedPlainText（§21）から Sync Marker の Highlight ID を取り出す。
// Highlight 本文にも khs: が現れうるので最後の一致を採る。Sync Marker が無ければ ok が false。
// Highlight ID の prefix は確かめない。
func SyncMarkerID(text string) (id string, ok bool) {
	matches := syncMarkerPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return "", false
	}
	return matches[len(matches)-1][1], true
}
