package sync

import (
	"time"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
)

// SelectTargets は §53 / §54 の sync の対象の Book を、books の順のまま返す。
// lastSynced は Book ID ごとの last_synced_at。キーが無い Book と値がゼロ値の Book は記録が無いものとして扱う。
// full のときは全 Book を返す。
func SelectTargets(books []domain.Book, lastSynced map[string]time.Time, full bool) []domain.Book {
	if full {
		return books
	}
	targets := make([]domain.Book, 0, len(books))
	for _, b := range books {
		if isTarget(b, lastSynced[b.ID]) {
			targets = append(targets, b)
		}
	}
	return targets
}

// isTarget は §53 の条件。LastAnnotatedAt は Kindle の日付のその日の 0 時 (UTC) なので、
// last_synced_at も UTC の暦日の 0 時に落とし、その前日以上の Book を対象にする。
func isTarget(b domain.Book, syncedAt time.Time) bool {
	if b.LastAnnotatedAt == nil || syncedAt.IsZero() {
		return true
	}
	u := syncedAt.UTC()
	since := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	return !b.LastAnnotatedAt.Before(since)
}
