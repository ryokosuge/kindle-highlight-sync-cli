package source

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

const (
	highlightsSelectorContainer         = "#kp-notebook-annotations"
	highlightsSelectorContainerRow      = "#kp-notebook-annotations > div.a-row.a-spacing-base"
	highlightsSelectorFragmentRow       = "body > div.a-row.a-spacing-base"
	highlightsSelectorText              = "div.kp-notebook-highlight span#highlight"
	highlightsSelectorEmptyText         = ".kp-notebook-highlight-empty-text"
	highlightsSelectorNote              = "div.kp-notebook-note span#note"
	highlightsSelectorLocation          = "input#kp-annotation-location"
	highlightsSelectorNextToken         = "input.kp-notebook-annotations-next-page-start"
	highlightsSelectorContentLimitState = "input.kp-notebook-content-limit-state"
)

// HighlightEntry は Highlight のページの 1 行。Highlight ID はまだ付いていない。
type HighlightEntry struct {
	RawID    string // 行の識別子（§18.2 の行の id 属性）。無ければ空
	Text     string // Note だけ・本文非表示なら空
	Note     string
	Location string
	Page     string
}

// HighlightsPage は Notebook の Highlight 1 ページ分の解析結果。
type HighlightsPage struct {
	Entries           []HighlightEntry
	NextToken         string // 空なら最後のページ
	ContentLimitState string // 続きのページの取得に渡す値
}

// ParseHighlightsPage は Notebook の Highlight 1 ページ分の HTML を解析する。
// 続きの印が無い、または本文も Note も空で本文非表示の印も無い行があれば SourceFormat の種類のエラーを返す（仕様書 §19 / §55）。
func ParseHighlightsPage(html []byte) (HighlightsPage, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return HighlightsPage{}, fmt.Errorf("source: highlights page: parse HTML: %w", err)
	}

	rowSelector := highlightsSelectorFragmentRow
	if doc.Find(highlightsSelectorContainer).Length() > 0 {
		rowSelector = highlightsSelectorContainerRow
	}

	next := doc.Find(highlightsSelectorNextToken).First()
	if next.Length() == 0 {
		return HighlightsPage{}, fmt.Errorf("source: highlights page: %w", newFormatError("next page token input is missing"))
	}
	token, ok := next.Attr("value")
	if !ok {
		return HighlightsPage{}, fmt.Errorf("source: highlights page: %w", newFormatError("next page token input has no value attribute"))
	}

	page := HighlightsPage{
		NextToken:         token,
		ContentLimitState: doc.Find(highlightsSelectorContentLimitState).First().AttrOr("value", ""),
	}
	var parseErr error
	doc.Find(rowSelector).EachWithBreak(func(i int, s *goquery.Selection) bool {
		entry, err := parseHighlightEntry(s)
		if err != nil {
			parseErr = fmt.Errorf("source: highlights page: row %d: %w", i+1, err)
			return false
		}
		page.Entries = append(page.Entries, entry)
		return true
	})
	if parseErr != nil {
		return HighlightsPage{}, parseErr
	}
	return page, nil
}

// parseHighlightEntry は 1 行を読む。Note だけの行は本文の div を持たず、本文非表示の行は本文の代わりに印を持つので（§18.7 / §19）、
// 本文の要素が無いことだけでは異常にせず、本文も Note も空で印も無いときだけ構造の異常にする（§19 / §18.9 (6)）。
func parseHighlightEntry(s *goquery.Selection) (HighlightEntry, error) {
	entry := HighlightEntry{
		RawID:    s.AttrOr("id", ""),
		Note:     strings.TrimSpace(s.Find(highlightsSelectorNote).First().Text()),
		Location: strings.TrimSpace(s.Find(highlightsSelectorLocation).First().AttrOr("value", "")),
	}
	emptyText := s.Find(highlightsSelectorEmptyText).Length() > 0
	if !emptyText {
		entry.Text = strings.TrimSpace(s.Find(highlightsSelectorText).First().Text())
	}
	if entry.Text == "" && entry.Note == "" && !emptyText {
		return HighlightEntry{}, newFormatError("highlight row has neither text nor note")
	}
	return entry, nil
}
