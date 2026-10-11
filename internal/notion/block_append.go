package notion

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// maxAppendChildren は Append block children の children の上限（https://developers.notion.com/reference/request-limits）。
const maxAppendChildren = 100

const (
	blockTypeQuote     = "quote"
	blockTypeParagraph = "paragraph"
)

// RichText は書き込む rich_text の要素 1 つ（type: text）。Content の長さは検査せずそのまま送る（分割は呼び出し側の担当。§22）。
type RichText struct {
	Content string
}

// ParagraphBlock は Quote Block の子にする paragraph block（§21 の Note）。
type ParagraphBlock struct {
	RichText []RichText
}

// QuoteBlock は Book のページに追記する Quote Block（§21）。Children は子の paragraph で、入れ子はこの 2 段まで。
type QuoteBlock struct {
	RichText []RichText
	Children []ParagraphBlock
}

type appendBlockChildrenRequest struct {
	Children []blockRequest `json:"children"`
}

type blockRequest struct {
	Type      string            `json:"type"`
	Quote     *quoteRequest     `json:"quote,omitempty"`
	Paragraph *paragraphRequest `json:"paragraph,omitempty"`
}

type quoteRequest struct {
	RichText []richTextRequest `json:"rich_text"`
	Children []blockRequest    `json:"children,omitempty"`
}

type paragraphRequest struct {
	RichText []richTextRequest `json:"rich_text"`
}

type appendBlockChildrenResponse struct {
	Object  *string            `json:"object"`
	Results *[]json.RawMessage `json:"results"`
}

// AppendBlockChildren は PATCH /v1/blocks/{id}/children で quotes をページの末尾に追記し、
// 作られた Quote Block の ID を quotes と同じ順で返す。要求は 1 回だけ送り、429 / 529 / 5xx / タイムアウトでも
// 再送しない（§29 の Highlight Append。結果不明時の再確認は呼び出し側の担当）。
// quotes が maxAppendChildren を超えると要求を送らずに error を返す。
// 応答の results の数が quotes の数と違えば、成功と数えずに error を返す。失敗はすべて exitcode.Notion。
func (c *Client) AppendBlockChildren(ctx context.Context, blockID string, quotes []QuoteBlock) ([]string, error) {
	path := "/v1/blocks/" + url.PathEscape(blockID) + "/children"
	if len(quotes) > maxAppendChildren {
		return nil, c.notionError(http.MethodPatch, path, fmt.Errorf("children must be at most %d blocks, got %d", maxAppendChildren, len(quotes)))
	}
	request := appendBlockChildrenRequest{Children: make([]blockRequest, 0, len(quotes))}
	for _, quote := range quotes {
		request.Children = append(request.Children, quoteBlockRequest(quote))
	}
	var response appendBlockChildrenResponse
	if err := c.doOnce(ctx, http.MethodPatch, path, request, &response); err != nil {
		return nil, err
	}
	return c.readAppendBlockChildrenResponse(path, &response, len(quotes))
}

// readAppendBlockChildrenResponse は応答を検証し、作られた block の ID を results の順で返す。
func (c *Client) readAppendBlockChildrenResponse(path string, response *appendBlockChildrenResponse, want int) ([]string, error) {
	invalid := func(format string, args ...any) error {
		return c.invalidResponse(http.MethodPatch, path, format, args...)
	}
	if response.Object == nil {
		return nil, invalid("object is required")
	}
	if *response.Object != "list" {
		return nil, invalid("object must be list")
	}
	if response.Results == nil {
		return nil, invalid("results is required")
	}
	if got := len(*response.Results); got != want {
		return nil, invalid("results has %d blocks, want %d", got, want)
	}
	ids := make([]string, 0, want)
	for i, result := range *response.Results {
		block, err := readBlock(result, fmt.Sprintf("results[%d]", i))
		if err != nil {
			return nil, invalid("%v", err)
		}
		if block.Type != blockTypeQuote {
			return nil, invalid("results[%d].type must be %s", i, blockTypeQuote)
		}
		ids = append(ids, block.ID)
	}
	return ids, nil
}

func quoteBlockRequest(quote QuoteBlock) blockRequest {
	request := &quoteRequest{RichText: richTextRequests(quote.RichText)}
	for _, paragraph := range quote.Children {
		request.Children = append(request.Children, blockRequest{
			Type:      blockTypeParagraph,
			Paragraph: &paragraphRequest{RichText: richTextRequests(paragraph.RichText)},
		})
	}
	return blockRequest{Type: blockTypeQuote, Quote: request}
}

// richTextRequests は items をそのまま rich_text の要素にする。空でも null ではなく [] を送る。
func richTextRequests(items []RichText) []richTextRequest {
	requests := make([]richTextRequest, 0, len(items))
	for _, item := range items {
		var request richTextRequest
		request.Type = "text"
		request.Text.Content = item.Content
		requests = append(requests, request)
	}
	return requests
}
