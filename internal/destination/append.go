package destination

import (
	"context"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/notion"
)

// Append block children の 1 回の要求の上限（https://developers.notion.com/reference/request-limits）。
// children は 100 block まで、body は 500KB まで。client は 100 件超を送らずに error にするが大きさは見ないので、
// body は notion.AppendBodySize で測り、どちらも AppendHighlights 側で収める。
const (
	maxAppendHighlights = 100
	maxAppendBodyBytes  = 500 * 1000
)

// AppendHighlights は highlights を受け取った順（§25）に renderHighlight で Quote Block にし、remote のページの末尾に追記する。
// 1 回の要求は maxAppendHighlights 件以下かつbody が maxAppendBodyBytes 以下になる範囲で先頭から貪欲に区切り、
// 順に送る。1 件だけで上限を超える Highlight はその 1 件で送り、Notion の 400 を失敗として返す。
// 戻り値は追記できた Highlight の Quote Block の Block ID と Highlight ID の組で、highlights と同じ順。
// 途中の要求が失敗したら残りの要求は送らず、それまでに成功した分と Notion の error（exitcode.Notion。
// HTTP status は *notion.StatusError で取れる）の両方を返す（§30）。再送は呼び出し側の担当（§29）。
// highlights が空なら要求を送らずに空を返す。照合の index には触らない。
func (d *NotionDestination) AppendHighlights(ctx context.Context, remote RemoteBook, highlights []domain.Highlight) ([]RemoteHighlight, error) {
	quotes := make([]notion.QuoteBlock, 0, len(highlights))
	for _, h := range highlights {
		quotes = append(quotes, renderHighlight(h))
	}
	appended := make([]RemoteHighlight, 0, len(highlights))
	for start := 0; start < len(quotes); {
		end := appendBatchEnd(quotes, start)
		ids, err := d.client.AppendBlockChildren(ctx, remote.ID, quotes[start:end])
		if err != nil {
			return appended, err
		}
		for i, id := range ids {
			appended = append(appended, RemoteHighlight{ID: id, HighlightID: highlights[start+i].ID})
		}
		start = end
	}
	return appended, nil
}

// appendBatchEnd は quotes[start:] のうち 1 回の要求で送る範囲の終端を返す。先頭の 1 件は上限を超えていても含める。
func appendBatchEnd(quotes []notion.QuoteBlock, start int) int {
	end := start + 1
	for end < len(quotes) && end-start < maxAppendHighlights && notion.AppendBodySize(quotes[start:end+1]) <= maxAppendBodyBytes {
		end++
	}
	return end
}
