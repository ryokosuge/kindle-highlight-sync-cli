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
func ParseHighlightsPage(html []byte) (HighlightsPage, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return HighlightsPage{}, fmt.Errorf("source: highlights page: parse HTML: %w", err)
	}

	rowSelector := highlightsSelectorFragmentRow
	if doc.Find(highlightsSelectorContainer).Length() > 0 {
		rowSelector = highlightsSelectorContainerRow
	}

	page := HighlightsPage{
		NextToken:         doc.Find(highlightsSelectorNextToken).First().AttrOr("value", ""),
		ContentLimitState: doc.Find(highlightsSelectorContentLimitState).First().AttrOr("value", ""),
	}
	doc.Find(rowSelector).Each(func(_ int, s *goquery.Selection) {
		page.Entries = append(page.Entries, parseHighlightEntry(s))
	})
	return page, nil
}

func parseHighlightEntry(s *goquery.Selection) HighlightEntry {
	entry := HighlightEntry{
		RawID:    s.AttrOr("id", ""),
		Note:     strings.TrimSpace(s.Find(highlightsSelectorNote).First().Text()),
		Location: strings.TrimSpace(s.Find(highlightsSelectorLocation).First().AttrOr("value", "")),
	}
	if s.Find(highlightsSelectorEmptyText).Length() == 0 {
		entry.Text = strings.TrimSpace(s.Find(highlightsSelectorText).First().Text())
	}
	return entry
}
