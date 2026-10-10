package notion

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

const blockChildrenPageSize = 100

// Block は Retrieve block children の応答の block のうち Sync Marker の照合に使う部分。
// Text は <type>.rich_text の plain_text を全要素つないだもの（rich_text を持たない種類は空）。
type Block struct {
	ID   string
	Type string
	Text string
}

type blockChildrenResponse struct {
	Object     *string            `json:"object"`
	Results    *[]json.RawMessage `json:"results"`
	HasMore    *bool              `json:"has_more"`
	NextCursor json.RawMessage    `json:"next_cursor"`
}

// ListBlockChildren は GET /v1/blocks/{id}/children を next_cursor が尽きるまで呼び、
// 直下の子 block を Notion が返した順に全件返す。孫は取らない。
// 途中の要求の失敗や一度使った next_cursor の再出現では一覧を返さない。失敗はすべて exitcode.Notion。
func (c *Client) ListBlockChildren(ctx context.Context, blockID string) ([]Block, error) {
	path := "/v1/blocks/" + url.PathEscape(blockID) + "/children"
	var blocks []Block
	requestPath := path + "?page_size=" + strconv.Itoa(blockChildrenPageSize)
	usedCursors := make(map[string]struct{})
	for {
		var response blockChildrenResponse
		if err := c.get(ctx, requestPath, &response); err != nil {
			return nil, err
		}
		results, nextCursor, err := c.readBlockChildrenResponse(path, &response)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, results...)
		if nextCursor == "" {
			return blocks, nil
		}
		if _, ok := usedCursors[nextCursor]; ok {
			return nil, c.invalidResponse(http.MethodGet, path, "next_cursor was already used")
		}
		usedCursors[nextCursor] = struct{}{}
		requestPath = path + "?page_size=" + strconv.Itoa(blockChildrenPageSize) + "&start_cursor=" + url.QueryEscape(nextCursor)
	}
}

// readBlockChildrenResponse は 1 回分の応答を検証し、block と次の要求の start_cursor（最後なら空）を返す。
func (c *Client) readBlockChildrenResponse(path string, response *blockChildrenResponse) ([]Block, string, error) {
	invalid := func(format string, args ...any) error {
		return c.invalidResponse(http.MethodGet, path, format, args...)
	}
	if response.Object == nil {
		return nil, "", invalid("object is required")
	}
	if *response.Object != "list" {
		return nil, "", invalid("object must be list")
	}
	if response.Results == nil {
		return nil, "", invalid("results is required")
	}
	if response.HasMore == nil {
		return nil, "", invalid("has_more is required")
	}
	if response.NextCursor == nil {
		return nil, "", invalid("next_cursor is required")
	}
	var nextCursor *string
	if err := json.Unmarshal(response.NextCursor, &nextCursor); err != nil {
		return nil, "", invalid("next_cursor must be a string or null")
	}
	if *response.HasMore {
		if nextCursor == nil {
			return nil, "", invalid("next_cursor must not be null when has_more is true")
		}
		if *nextCursor == "" {
			return nil, "", invalid("next_cursor must not be empty when has_more is true")
		}
	}

	blocks := make([]Block, 0, len(*response.Results))
	for i, result := range *response.Results {
		block, err := readBlock(result, fmt.Sprintf("results[%d]", i))
		if err != nil {
			return nil, "", invalid("%v", err)
		}
		blocks = append(blocks, block)
	}
	if !*response.HasMore {
		return blocks, "", nil
	}
	return blocks, *nextCursor, nil
}

// readBlock は応答の block を検証し、ID / Type / Text を取り出す。
// <type> のキーが無い、または rich_text を持たない種類は Text を空にする（block の種類は多岐にわたるので拒否しない）。
func readBlock(raw json.RawMessage, at string) (Block, error) {
	var result struct {
		Object *string `json:"object"`
		ID     *string `json:"id"`
		Type   *string `json:"type"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return Block{}, fmt.Errorf("%s must be an object", at)
	}
	if result.Object == nil {
		return Block{}, fmt.Errorf("%s.object is required", at)
	}
	if *result.Object != "block" {
		return Block{}, fmt.Errorf("%s.object must be block", at)
	}
	if result.ID == nil {
		return Block{}, fmt.Errorf("%s.id is required", at)
	}
	if *result.ID == "" {
		return Block{}, fmt.Errorf("%s.id must not be empty", at)
	}
	if result.Type == nil {
		return Block{}, fmt.Errorf("%s.type is required", at)
	}
	if *result.Type == "" {
		return Block{}, fmt.Errorf("%s.type must not be empty", at)
	}
	block := Block{ID: *result.ID, Type: *result.Type}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Block{}, fmt.Errorf("%s must be an object", at)
	}
	typeRaw, ok := fields[*result.Type]
	if !ok {
		return block, nil
	}
	var typeObject struct {
		RichText json.RawMessage `json:"rich_text"`
	}
	if err := json.Unmarshal(typeRaw, &typeObject); err != nil {
		return Block{}, fmt.Errorf("%s.%s must be an object", at, *result.Type)
	}
	if typeObject.RichText == nil {
		return block, nil
	}
	var items *[]richTextResponse
	if err := json.Unmarshal(typeObject.RichText, &items); err != nil {
		return Block{}, fmt.Errorf("%s.%s.rich_text must be an array", at, *result.Type)
	}
	text, err := joinPlainText(items, at+"."+*result.Type+".rich_text")
	if err != nil {
		return Block{}, err
	}
	block.Text = text
	return block, nil
}
