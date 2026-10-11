package notion

import (
	"context"
	"net/http"
	"net/url"
)

// PropertyInput は書き込む Property の値。TitleInput / RichTextInput / DateInput で作る。
// 値の中身は検査せずそのまま送る（文字数の分割は呼び出し側の担当）。
type PropertyInput struct {
	typ, text, date string
}

// TitleInput は title Property に書き込む値を作る。
func TitleInput(text string) PropertyInput {
	return PropertyInput{typ: typeTitle, text: text}
}

// RichTextInput は rich_text Property に書き込む値を作る。
func RichTextInput(text string) PropertyInput {
	return PropertyInput{typ: typeRichText, text: text}
}

// DateInput は date Property に書き込む値を作る。
func DateInput(start string) PropertyInput {
	return PropertyInput{typ: typeDate, date: start}
}

type richTextRequest struct {
	Type string `json:"type"`
	Text struct {
		Content string `json:"content"`
	} `json:"text"`
}

type titlePropertyRequest struct {
	Title []richTextRequest `json:"title"`
}

type richTextPropertyRequest struct {
	RichText []richTextRequest `json:"rich_text"`
}

type datePropertyRequest struct {
	Date *dateRequest `json:"date"`
}

type dateRequest struct {
	Start string `json:"start"`
}

type pageWriteResponse struct {
	Object *string `json:"object"`
	ID     *string `json:"id"`
}

type createPageRequest struct {
	Parent struct {
		Type         string `json:"type"`
		DataSourceID string `json:"data_source_id"`
	} `json:"parent"`
	Properties map[string]any `json:"properties"`
}

type updatePageRequest struct {
	Properties map[string]any `json:"properties"`
}

// CreatePage は Data Source に page を作り、その ID を返す。
func (c *Client) CreatePage(ctx context.Context, dataSourceID string, properties map[string]PropertyInput) (string, error) {
	const path = "/v1/pages"
	body := createPageRequest{Properties: propertiesRequest(properties)}
	body.Parent.Type = "data_source_id"
	body.Parent.DataSourceID = dataSourceID
	var response pageWriteResponse
	if err := c.do(ctx, http.MethodPost, path, body, &response); err != nil {
		return "", err
	}
	return c.readPageID(http.MethodPost, path, response)
}

// UpdatePage は page の Property を更新する。
func (c *Client) UpdatePage(ctx context.Context, pageID string, properties map[string]PropertyInput) error {
	path := "/v1/pages/" + url.PathEscape(pageID)
	var response pageWriteResponse
	if err := c.do(ctx, http.MethodPatch, path, updatePageRequest{Properties: propertiesRequest(properties)}, &response); err != nil {
		return err
	}
	_, err := c.readPageID(http.MethodPatch, path, response)
	return err
}

func (c *Client) readPageID(method, path string, r pageWriteResponse) (string, error) {
	if r.Object == nil {
		return "", c.invalidResponse(method, path, "object is required")
	}
	if *r.Object != "page" {
		return "", c.invalidResponse(method, path, "object must be page")
	}
	if r.ID == nil {
		return "", c.invalidResponse(method, path, "id is required")
	}
	if *r.ID == "" {
		return "", c.invalidResponse(method, path, "id must not be empty")
	}
	return *r.ID, nil
}

func propertiesRequest(properties map[string]PropertyInput) map[string]any {
	request := make(map[string]any, len(properties))
	for id, property := range properties {
		switch property.typ {
		case typeTitle:
			var title []richTextRequest
			if property.text != "" {
				title = []richTextRequest{newRichTextRequest(property.text)}
			} else {
				title = []richTextRequest{}
			}
			request[id] = titlePropertyRequest{Title: title}
		case typeRichText:
			var richText []richTextRequest
			if property.text != "" {
				richText = []richTextRequest{newRichTextRequest(property.text)}
			} else {
				richText = []richTextRequest{}
			}
			request[id] = richTextPropertyRequest{RichText: richText}
		case typeDate:
			var date *dateRequest
			if property.date != "" {
				date = &dateRequest{Start: property.date}
			}
			request[id] = datePropertyRequest{Date: date}
		}
	}
	return request
}

func newRichTextRequest(text string) richTextRequest {
	var request richTextRequest
	request.Type = "text"
	request.Text.Content = text
	return request
}
