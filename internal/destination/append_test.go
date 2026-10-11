package destination

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/notion"
)

const testAppendLine = "PATCH " + testBlocksPath

// appendHighlights は a-highlight-1 .. a-highlight-n の Highlight を順に作る。
func appendHighlights(n int) []domain.Highlight {
	hs := make([]domain.Highlight, 0, n)
	for i := 1; i <= n; i++ {
		hs = append(hs, domain.Highlight{ID: fmt.Sprintf("a-highlight-%d", i), Text: "本文", Location: fmt.Sprint(i)})
	}
	return hs
}

// appendReply は Append block children の応答。highlights の Quote Block が blk-<Highlight ID> の ID で作られた形。
func appendReply(highlights []domain.Highlight) notionReply {
	blocks := make([]string, 0, len(highlights))
	for _, h := range highlights {
		blocks = append(blocks, blockJSON("blk-"+h.ID, "quote", "khs:"+h.ID))
	}
	return queryReply("", blocks...)
}

// wantRemote は appendReply の応答に対する戻り値。
func wantRemote(highlights []domain.Highlight) []RemoteHighlight {
	remotes := make([]RemoteHighlight, 0, len(highlights))
	for _, h := range highlights {
		remotes = append(remotes, RemoteHighlight{ID: "blk-" + h.ID, HighlightID: h.ID})
	}
	return remotes
}

// wantQuote は Notion client が送る Quote Block の JSON を any にしたもの（block_append_test.go の形）。
func wantQuote(contents ...string) any {
	richText := make([]any, 0, len(contents))
	for _, c := range contents {
		richText = append(richText, map[string]any{"type": "text", "text": map[string]any{"content": c}})
	}
	return map[string]any{"type": "quote", "quote": map[string]any{"rich_text": richText}}
}

// decodeChildren は要求の body から children を取り出す。
func decodeChildren(t *testing.T, body string) []any {
	t.Helper()
	var decoded struct {
		Children []any `json:"children"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("json.Unmarshal(body) error = %v", err)
	}
	return decoded.Children
}

// childMarkers は要求の body の children の Sync Marker を順に返す（どの Highlight をどの順で送ったかを見る）。
func childMarkers(t *testing.T, body string) []string {
	t.Helper()
	var decoded struct {
		Children []struct {
			Quote struct {
				RichText []struct {
					Text struct {
						Content string `json:"content"`
					} `json:"text"`
				} `json:"rich_text"`
			} `json:"quote"`
		} `json:"children"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("json.Unmarshal(body) error = %v", err)
	}
	markers := make([]string, 0, len(decoded.Children))
	for _, child := range decoded.Children {
		last := child.Quote.RichText[len(child.Quote.RichText)-1].Text.Content
		markers = append(markers, last[strings.LastIndex(last, syncMarkerPrefix):])
	}
	return markers
}

func markers(highlights []domain.Highlight) []string {
	ms := make([]string, 0, len(highlights))
	for _, h := range highlights {
		ms = append(ms, syncMarkerPrefix+h.ID)
	}
	return ms
}

func TestNotionDestinationAppendHighlights(t *testing.T) {
	highlights := []domain.Highlight{
		{ID: "a-highlight-1", Text: "本文", Note: "メモ", Location: "123", Page: "45"},
		{ID: "a-note-2", Note: "Note だけ", Location: "200"},
		{ID: "a-highlight-3", Text: "最後", Location: "300"},
	}
	d, requests := newWriteDestination(t, fullMapping, appendReply(highlights))

	got, err := d.AppendHighlights(context.Background(), RemoteBook{ID: testPageID}, highlights)
	if err != nil {
		t.Fatalf("AppendHighlights() error = %v", err)
	}
	if want := wantRemote(highlights); !reflect.DeepEqual(got, want) {
		t.Errorf("AppendHighlights() = %+v, want %+v", got, want)
	}

	// 受け取った順のまま、renderHighlight の形（本文・改行付きの Location / Page / Sync Marker・Note の子）で 1 回の要求に入る
	reqs := requests()
	assertRequestLines(t, reqs, testAppendLine)
	quote1 := wantQuote("本文", "\nLocation 123 · Page 45 · khs:a-highlight-1").(map[string]any)
	quote1["quote"].(map[string]any)["children"] = []any{map[string]any{
		"type":      "paragraph",
		"paragraph": map[string]any{"rich_text": []any{map[string]any{"type": "text", "text": map[string]any{"content": "メモ"}}}},
	}}
	wantChildren := []any{
		quote1,
		wantQuote("Note だけ", "\nLocation 200 · khs:a-note-2"),
		wantQuote("最後", "\nLocation 300 · khs:a-highlight-3"),
	}
	if children := decodeChildren(t, reqs[0].body); !reflect.DeepEqual(children, wantChildren) {
		t.Errorf("body.children = %v, want %v", children, wantChildren)
	}
}

func TestNotionDestinationAppendHighlightsEmpty(t *testing.T) {
	d, requests := newWriteDestination(t, fullMapping)

	got, err := d.AppendHighlights(context.Background(), RemoteBook{ID: testPageID}, nil)
	if err != nil {
		t.Fatalf("AppendHighlights() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("AppendHighlights() = %+v, want empty", got)
	}
	assertRequestLines(t, requests())
}

// TestNotionDestinationAppendHighlightsSplit は 100 件以下かつ body 500KB 以下の単位に分かれ、全件が順に追記されることを確かめる。
func TestNotionDestinationAppendHighlightsSplit(t *testing.T) {
	// 本文が size 文字の Highlight を n 件。150,000 文字なら 75 要素 + Sync Marker の要素に描画され、body では約 150KB になる
	large := func(n, size int) []domain.Highlight {
		hs := appendHighlights(n)
		for i := range hs {
			hs[i].Text = strings.Repeat("a", size)
		}
		return hs
	}
	tests := []struct {
		name       string
		highlights []domain.Highlight
		wantSizes  []int // 要求ごとの children の数
	}{
		{
			name:       "100 件なら 1 回で送る",
			highlights: appendHighlights(100),
			wantSizes:  []int{100},
		},
		{
			name:       "101 件以上は 100 件以下ずつに分ける",
			highlights: appendHighlights(250),
			wantSizes:  []int{100, 100, 50},
		},
		{
			name:       "body が 500KB を超えないところで分ける",
			highlights: large(5, 150_000),
			wantSizes:  []int{3, 2},
		},
		{
			name:       "1 件だけで 500KB を超える Highlight は弾かずにその 1 件だけで送る",
			highlights: append(large(1, 600_000), appendHighlights(1)...),
			wantSizes:  []int{1, 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			replies := make([]notionReply, 0, len(tt.wantSizes))
			wantLines := make([]string, 0, len(tt.wantSizes))
			for start, n := 0, 0; n < len(tt.wantSizes); n++ {
				replies = append(replies, appendReply(tt.highlights[start:start+tt.wantSizes[n]]))
				wantLines = append(wantLines, testAppendLine)
				start += tt.wantSizes[n]
			}
			d, requests := newWriteDestination(t, fullMapping, replies...)

			got, err := d.AppendHighlights(context.Background(), RemoteBook{ID: testPageID}, tt.highlights)
			if err != nil {
				t.Fatalf("AppendHighlights() error = %v", err)
			}
			if want := wantRemote(tt.highlights); !reflect.DeepEqual(got, want) {
				t.Errorf("AppendHighlights() = %d highlights, want %d in order", len(got), len(want))
			}
			reqs := requests()
			assertRequestLines(t, reqs, wantLines...)
			for i, start := 0, 0; i < len(reqs) && i < len(tt.wantSizes); i++ {
				if got, want := childMarkers(t, reqs[i].body), markers(tt.highlights[start:start+tt.wantSizes[i]]); !reflect.DeepEqual(got, want) {
					t.Errorf("requests[%d] markers = %d markers %v..., want %d markers %v...", i, len(got), got[:min(2, len(got))], len(want), want[:min(2, len(want))])
				}
				if size := len(reqs[i].body); size > 500*1000 && tt.wantSizes[i] > 1 {
					t.Errorf("requests[%d] body = %d bytes, want <= 500KB", i, size)
				}
				start += tt.wantSizes[i]
			}
		})
	}
}

// TestNotionDestinationAppendHighlightsFailure は途中の要求が失敗したら、それより前に成功した分と #124 の error の両方が返り、
// 残りの要求は送られないことを確かめる。
func TestNotionDestinationAppendHighlightsFailure(t *testing.T) {
	highlights := appendHighlights(250)
	failure := notionReply{status: http.StatusBadRequest, body: notionErrorBody}
	tests := []struct {
		name         string
		replies      []notionReply
		wantAppended []RemoteHighlight
	}{
		{
			name:         "最初の要求が失敗したら成功分は空",
			replies:      []notionReply{failure},
			wantAppended: []RemoteHighlight{},
		},
		{
			name:         "2 回目の要求が失敗したら 1 回目の 100 件だけを返し 3 回目は送らない",
			replies:      []notionReply{appendReply(highlights[:100]), failure},
			wantAppended: wantRemote(highlights[:100]),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, requests := newWriteDestination(t, fullMapping, tt.replies...)

			got, err := d.AppendHighlights(context.Background(), RemoteBook{ID: testPageID}, highlights)
			if code := exitcode.Code(err); code != 4 {
				t.Fatalf("exitcode.Code(err) = %d, want 4 (err = %v)", code, err)
			}
			var status *notion.StatusError
			if !errors.As(err, &status) || status.Status != http.StatusBadRequest {
				t.Errorf("StatusError = %+v, want Status 400", status)
			}
			if !reflect.DeepEqual(got, tt.wantAppended) {
				t.Errorf("AppendHighlights() = %d highlights, want %d", len(got), len(tt.wantAppended))
			}
			wantLines := make([]string, len(tt.replies))
			for i := range wantLines {
				wantLines[i] = testAppendLine
			}
			assertRequestLines(t, requests(), wantLines...)
		})
	}
}

// TestNotionDestinationAppendThenFindBook は追記しても同じ NotionDestination の照合の結果が変わらず、Data Source も取り直さないことを確かめる。
func TestNotionDestinationAppendThenFindBook(t *testing.T) {
	exactBook := kindleBook(testASIN, "Kindle ID Page", "Alan")
	adoptableBook := kindleBook(testOtherASIN, "ASIN Page", "Alan")
	highlights := appendHighlights(2)
	d, requests := newWriteDestination(t, fullMapping,
		queryReply("",
			bookPage(testPageID, testASIN, "", "Kindle ID Page", "Alan"),
			bookPage("page-asin", "", testOtherASIN, "ASIN Page", "Alan"),
		),
		appendReply(highlights),
	)

	before, err := d.FindBook(context.Background(), exactBook)
	if err != nil {
		t.Fatalf("FindBook() error = %v", err)
	}
	assertMatch(t, before, "exact", testPageID)

	if _, err := d.AppendHighlights(context.Background(), *before.Book, highlights); err != nil {
		t.Fatalf("AppendHighlights() error = %v", err)
	}

	after, err := d.FindBook(context.Background(), exactBook)
	if err != nil {
		t.Fatalf("FindBook() after append error = %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Errorf("FindBook() after append = %+v, want %+v", after, before)
	}
	other, err := d.FindBook(context.Background(), adoptableBook)
	if err != nil {
		t.Fatalf("FindBook() of another book error = %v", err)
	}
	assertMatch(t, other, "adoptable", "page-asin")
	assertRequestLines(t, requests(), "POST "+testQueryPath, testAppendLine)
}
