package main

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"

	syncengine "github.com/ryokosuge/kindle-highlight-sync-cli/internal/sync"
)

// writePlan は Plan の照合結果と summary の表を w に書く（仕様書 §64）。
// stdout は redact を通さないので、ページ ID と件数だけを出して Highlight 本文や token は書かない（§74）。
func writePlan(w io.Writer, p syncengine.Plan) error {
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "Kindle Book\tMatch Status\tMatched Notion Page\tAction\tHighlights to append")
	for _, b := range p.Books {
		pageID := "-"
		if b.Match.Book != nil {
			pageID = b.Match.Book.ID
		}
		appendCount := "-"
		if b.Action != syncengine.ActionSkip {
			appendCount = strconv.Itoa(b.HighlightsToAppend)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", b.Book.Title, b.Match.Status, pageID, b.Action, appendCount)
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

// writeApplyResult は実行の結果の件数を w に書く。
// stdout は redact を通さないので件数だけを出し、Book のタイトルや Book ID・error の本文は書かない（§74）。
func writeApplyResult(w io.Writer, r syncengine.ApplyResult) error {
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "Created\t%d\n", len(r.Created))
	_, _ = fmt.Fprintf(tw, "Adopted\t%d\n", len(r.Adopted))
	_, _ = fmt.Fprintf(tw, "Appended highlights\t%d\n", r.Appended)
	if r.Rechecked > 0 {
		// 確かめ直しが起きた run だけ出す。起きなかった run の出力は変えない
		_, _ = fmt.Fprintf(tw, "Rechecked books\t%d\n", r.Rechecked)
		_, _ = fmt.Fprintf(tw, "Resent highlights\t%d\n", r.Resent)
	}
	_, _ = fmt.Fprintf(tw, "Skipped (ambiguous)\t%d\n", len(r.Skipped))
	_, _ = fmt.Fprintf(tw, "Failed\t%d\n", len(r.Failed))
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// writeTargetCount は sync の対象になった Book の数と全 Book の数を w に書く（§53）。
// stdout は redact を通さないので件数だけを出す（§74）。
func writeTargetCount(w io.Writer, targets, total int) error {
	_, err := fmt.Fprintf(w, "Target books  %d of %d\n", targets, total)
	return err
}
