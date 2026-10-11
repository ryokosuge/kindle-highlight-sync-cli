package destination

import (
	"context"
	"fmt"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/notion"
)

type pageQuerier interface {
	QueryDataSource(ctx context.Context, dataSourceID string) ([]notion.Page, error)
	ListBlockChildren(ctx context.Context, blockID string) ([]notion.Block, error)
	CreatePage(ctx context.Context, dataSourceID string, properties map[string]notion.PropertyInput) (string, error)
	UpdatePage(ctx context.Context, pageID string, properties map[string]notion.PropertyInput) error
}

const quoteBlockType = "quote"

// NotionDestination は Notion の Data Source を同期先とする Destination。同時に複数の goroutine から呼ばない。
type NotionDestination struct {
	client pageQuerier
	notion config.Notion
	index  *BookIndex // 最初の FindBook / CreateBook / AdoptBook で作る
}

var _ Destination = (*NotionDestination)(nil)

// NewNotionDestination は cfg の Data Source と Property の対応を使う NotionDestination を返す。
func NewNotionDestination(client *notion.Client, cfg config.Notion) *NotionDestination {
	return &NotionDestination{client: client, notion: cfg}
}

// FindBook は index をまだ作っていなければ作ってから照合する（§10）。Data Source を取り直すことはない。
func (d *NotionDestination) FindBook(ctx context.Context, book domain.Book) (BookMatch, error) {
	if err := d.ensureIndex(ctx); err != nil {
		return BookMatch{}, err
	}
	return d.index.Match(book), nil
}

// ensureIndex は index が無ければ Data Source の全ページを取って作る。CreateBook / AdoptBook で書いたページは
// index にだけ反映するので、同じ NotionDestination では全件取得は 1 回で済む。
// 全件取得に失敗したら index を作らずに error（exitcode.Notion）を返し、次の呼び出しで取り直す。
func (d *NotionDestination) ensureIndex(ctx context.Context) error {
	if d.index != nil {
		return nil
	}
	pages, err := d.client.QueryDataSource(ctx, d.notion.DataSourceID)
	if err != nil {
		return err
	}
	d.index = NewBookIndex(indexedBooks(pages, d.notion.Mapping), d.notion.Mapping.Author != "")
	return nil
}

// ListHighlightIDs は Book のページの直下の子 block を全件取り、Sync Marker のある Quote Block（§21）を
// Highlight ID で引ける map にして返す。Notion へは読み取りの要求しか送らない。
// 同じ Highlight ID の Quote Block が複数あればページ順で最初の block を採る。
// 子 block の取得に失敗したら途中までの map を返さず error（exitcode.Notion）をそのまま返す。
// Sync Marker の Highlight ID の prefix が採用方式（domain.HighlightIDPrefix）と違えば、方式の混在（§16.3）として
// 最初に見つけた block で止め、map を返さず exitcode.LocalState の error を返す。メッセージには prefix と Notion Page ID だけを入れる。
func (d *NotionDestination) ListHighlightIDs(ctx context.Context, remote RemoteBook) (map[string]RemoteHighlight, error) {
	blocks, err := d.client.ListBlockChildren(ctx, remote.ID)
	if err != nil {
		return nil, err
	}
	highlights := make(map[string]RemoteHighlight)
	for _, b := range blocks {
		if b.Type != quoteBlockType {
			continue
		}
		id, ok := SyncMarkerID(b.Text)
		if !ok {
			continue
		}
		if !domain.IsAdoptedHighlightID(id) {
			return nil, exitcode.New(exitcode.LocalState, fmt.Errorf(
				"notion page %s has a Sync Marker with Highlight ID prefix %q, but the adopted prefix is %q",
				remote.ID, domain.HighlightIDPrefixOf(id), domain.HighlightIDPrefix))
		}
		if _, dup := highlights[id]; dup {
			continue
		}
		highlights[id] = RemoteHighlight{ID: b.ID, HighlightID: id}
	}
	return highlights, nil
}

// indexedBooks は各ページの Property を mapping の Property ID で読む。
// mapping されていない Property（ID が空）と、ページに無い Property は空として扱う。
func indexedBooks(pages []notion.Page, mapping config.Mapping) []IndexedBook {
	books := make([]IndexedBook, 0, len(pages))
	for _, p := range pages {
		text := func(propertyID string) string { return p.Properties[propertyID].Text }
		books = append(books, IndexedBook{
			Remote:   RemoteBook{ID: p.ID},
			KindleID: text(mapping.KindleID),
			ASIN:     text(mapping.ASIN),
			Title:    text(mapping.Title),
			Author:   text(mapping.Author),
		})
	}
	return books
}
