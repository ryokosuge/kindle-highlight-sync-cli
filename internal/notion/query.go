package notion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
)

const queryPageSize = 100

// Page は Query a data source の応答の page のうち照合に使う部分。Properties の key は Property ID。
type Page struct {
	ID         string
	Properties map[string]PropertyValue
}

// PropertyValue は page の Property の値。Text は title / rich_text の plain_text を全要素つないだもの、
// Date は date の start（空の date なら空文字列）。それ以外の型では Type だけが入る。
type PropertyValue struct {
	Type string
	Text string
	Date string
}

type queryRequest struct {
	PageSize    int    `json:"page_size"`
	StartCursor string `json:"start_cursor,omitempty"`
}

type queryResponse struct {
	Object        *string         `json:"object"`
	Results       *[]pageResponse `json:"results"`
	HasMore       *bool           `json:"has_more"`
	NextCursor    json.RawMessage `json:"next_cursor"`
	RequestStatus *struct {
		Type             *string `json:"type"`
		IncompleteReason *string `json:"incomplete_reason"`
	} `json:"request_status"`
}

type pageResponse struct {
	Object     *string                           `json:"object"`
	ID         *string                           `json:"id"`
	Properties *map[string]propertyValueResponse `json:"properties"`
}

type propertyValueResponse struct {
	ID       *string             `json:"id"`
	Type     *string             `json:"type"`
	Title    *[]richTextResponse `json:"title"`
	RichText *[]richTextResponse `json:"rich_text"`
	Date     json.RawMessage     `json:"date"`
}

type richTextResponse struct {
	PlainText *string `json:"plain_text"`
}

// QueryDataSource は POST /v1/data_sources/{id}/query を next_cursor が尽きるまで呼び、全ページを返す。
// 途中の要求の失敗や request_status の incomplete では一覧を返さない。失敗はすべて exitcode.Notion。
func (c *Client) QueryDataSource(ctx context.Context, dataSourceID string) ([]Page, error) {
	path := "/v1/data_sources/" + url.PathEscape(dataSourceID) + "/query"
	var pages []Page
	request := queryRequest{PageSize: queryPageSize}
	for {
		var response queryResponse
		if err := c.do(ctx, http.MethodPost, path, request, &response); err != nil {
			return nil, err
		}
		results, nextCursor, err := c.readQueryResponse(path, &response)
		if err != nil {
			return nil, err
		}
		pages = append(pages, results...)
		if nextCursor == "" {
			return pages, nil
		}
		request.StartCursor = nextCursor
	}
}

// readQueryResponse は 1 回分の応答を検証し、ページと次の要求の start_cursor（最後なら空）を返す。
func (c *Client) readQueryResponse(path string, response *queryResponse) ([]Page, string, error) {
	invalid := func(format string, args ...any) error {
		return c.invalidResponse(http.MethodPost, path, format, args...)
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
	if status := response.RequestStatus; status != nil {
		if status.Type == nil {
			return nil, "", invalid("request_status.type is required")
		}
		switch *status.Type {
		case "complete":
		case "incomplete":
			message := "query result is incomplete"
			if status.IncompleteReason != nil && *status.IncompleteReason != "" {
				message += ": " + *status.IncompleteReason
			}
			return nil, "", c.notionError(http.MethodPost, path, errors.New(message))
		default:
			return nil, "", invalid("request_status.type must be complete or incomplete")
		}
	}

	pages := make([]Page, 0, len(*response.Results))
	for i, result := range *response.Results {
		page, err := readPage(result, fmt.Sprintf("results[%d]", i))
		if err != nil {
			return nil, "", invalid("%v", err)
		}
		pages = append(pages, page)
	}
	if !*response.HasMore {
		return pages, "", nil
	}
	return pages, *nextCursor, nil
}

// readPage は応答の page を検証し、Property 名 → 値の properties を Property ID → 値に組み替える。
func readPage(result pageResponse, at string) (Page, error) {
	if result.Object == nil {
		return Page{}, fmt.Errorf("%s.object is required", at)
	}
	if *result.Object != "page" {
		return Page{}, fmt.Errorf("%s.object must be page", at)
	}
	if result.ID == nil {
		return Page{}, fmt.Errorf("%s.id is required", at)
	}
	if *result.ID == "" {
		return Page{}, fmt.Errorf("%s.id must not be empty", at)
	}
	if result.Properties == nil {
		return Page{}, fmt.Errorf("%s.properties is required", at)
	}

	names := make([]string, 0, len(*result.Properties))
	for name := range *result.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	page := Page{ID: *result.ID, Properties: make(map[string]PropertyValue, len(names))}
	for _, name := range names {
		property := (*result.Properties)[name]
		propertyAt := fmt.Sprintf("%s.properties[%q]", at, name)
		if property.ID == nil {
			return Page{}, fmt.Errorf("%s.id is required", propertyAt)
		}
		if *property.ID == "" {
			return Page{}, fmt.Errorf("%s.id must not be empty", propertyAt)
		}
		if _, ok := page.Properties[*property.ID]; ok {
			return Page{}, fmt.Errorf("%s.properties contains duplicate id", at)
		}
		value, err := readPropertyValue(property, propertyAt)
		if err != nil {
			return Page{}, err
		}
		page.Properties[*property.ID] = value
	}
	return page, nil
}

func readPropertyValue(property propertyValueResponse, at string) (PropertyValue, error) {
	if property.Type == nil {
		return PropertyValue{}, fmt.Errorf("%s.type is required", at)
	}
	if *property.Type == "" {
		return PropertyValue{}, fmt.Errorf("%s.type must not be empty", at)
	}
	value := PropertyValue{Type: *property.Type}
	switch value.Type {
	case "title":
		text, err := joinPlainText(property.Title, at+".title")
		if err != nil {
			return PropertyValue{}, err
		}
		value.Text = text
	case "rich_text":
		text, err := joinPlainText(property.RichText, at+".rich_text")
		if err != nil {
			return PropertyValue{}, err
		}
		value.Text = text
	case "date":
		date, err := readDateStart(property.Date, at+".date")
		if err != nil {
			return PropertyValue{}, err
		}
		value.Date = date
	}
	return value, nil
}

func joinPlainText(items *[]richTextResponse, at string) (string, error) {
	if items == nil {
		return "", fmt.Errorf("%s is required", at)
	}
	var text string
	for i, item := range *items {
		if item.PlainText == nil {
			return "", fmt.Errorf("%s[%d].plain_text is required", at, i)
		}
		text += *item.PlainText
	}
	return text, nil
}

// readDateStart は date の start を返す。date は空の Property では null になるので、そのときは空文字列。
func readDateStart(raw json.RawMessage, at string) (string, error) {
	if raw == nil {
		return "", fmt.Errorf("%s is required", at)
	}
	var date *struct {
		Start *string `json:"start"`
	}
	if err := json.Unmarshal(raw, &date); err != nil {
		return "", fmt.Errorf("%s must be an object or null", at)
	}
	if date == nil {
		return "", nil
	}
	if date.Start == nil {
		return "", fmt.Errorf("%s.start is required", at)
	}
	return *date.Start, nil
}
