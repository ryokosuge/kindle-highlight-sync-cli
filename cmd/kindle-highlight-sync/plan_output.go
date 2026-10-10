package main

import (
	"bytes"
	"fmt"
	"io"
	"text/tabwriter"

	syncengine "github.com/ryokosuge/kindle-highlight-sync-cli/internal/sync"
)

// writePlan は Plan の照合結果と summary の表を w に書く（仕様書 §64）。
// stdout は redact を通さないので、ページ ID と件数だけを出して Highlight 本文や token は書かない（§74）。
func writePlan(w io.Writer, p syncengine.Plan) error {
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "Kindle Book\tMatch Status\tMatched Notion Page\tAction")
	for _, b := range p.Books {
		pageID := "-"
		if b.Match.Book != nil {
			pageID = b.Match.Book.ID
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", b.Book.Title, b.Match.Status, pageID, b.Action)
	}
	_, _ = fmt.Fprintln(tw)
	_, _ = fmt.Fprintf(tw, "Exact matches\t%d\n", p.Summary.ExactMatches)
	_, _ = fmt.Fprintf(tw, "Adoptable matches\t%d\n", p.Summary.AdoptableMatches)
	_, _ = fmt.Fprintf(tw, "New books\t%d\n", p.Summary.NewBooks)
	_, _ = fmt.Fprintf(tw, "Ambiguous\t%d\n", p.Summary.Ambiguous)
	_, _ = fmt.Fprintf(tw, "Highlights to append\t%d\n", p.Summary.HighlightsToAppend)
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}
