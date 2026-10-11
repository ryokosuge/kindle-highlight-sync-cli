package destination

import (
	"context"
	"time"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/notion"
)

// lastAnnotatedAtLayout は last_annotated_at Property に書く日付の形。Kindle の Last Annotated Date は日付だけ（§18.3）なので時刻を付けない。
const lastAnnotatedAtLayout = time.DateOnly

// CreateBook は mapping のある Property に book の title / author / asin / Book ID（Kindle ID Property）/ last_annotated_at を
// 書いたページを Data Source に作り（§12 の Create 列）、作成したページを index に足して返す。
// mapping の無い Property と、book で空の author / ASIN / LastAnnotatedAt は body に入れない。
// 呼び出し側は照合が none の Book にだけ使う前提（同じ Book に 2 回呼ぶと、その Book ID の照合は ambiguous になる）。
// index をまだ作っていなければ先に作る。作成に失敗したら Notion の error（exitcode.Notion）をそのまま返し、index は変えない。
func (d *NotionDestination) CreateBook(ctx context.Context, book domain.Book) (RemoteBook, error) {
	if err := d.ensureIndex(ctx); err != nil {
		return RemoteBook{}, err
	}
	id, err := d.client.CreatePage(ctx, d.notion.DataSourceID, d.createProperties(book))
	if err != nil {
		return RemoteBook{}, err
	}
	remote := RemoteBook{ID: id}
	d.index.AddCreated(remote, book.ID)
	return remote, nil
}

// AdoptBook は remote の Kindle ID Property にだけ book の Book ID を書き（§11）、そのページを book と exact で一致するものとして index に反映する。
// title / author / asin / last_annotated_at は書かない（§12 の Adopt 列の author / asin / last_annotated_at は後続の Task）。
// 呼び出し側は照合が adoptable の Book と、その Match が返したページにだけ使う前提。
// index をまだ作っていなければ先に作る。更新に失敗したら Notion の error（exitcode.Notion）をそのまま返し、index は変えない。
func (d *NotionDestination) AdoptBook(ctx context.Context, remote RemoteBook, book domain.Book) error {
	if err := d.ensureIndex(ctx); err != nil {
		return err
	}
	properties := map[string]notion.PropertyInput{d.notion.Mapping.KindleID: notion.RichTextInput(book.ID)}
	if err := d.client.UpdatePage(ctx, remote.ID, properties); err != nil {
		return err
	}
	d.index.Adopt(remote, book.ID)
	return nil
}

// createProperties は作成するページの Property を mapping の Property ID を key に組む（indexedBooks と同じ ID）。
func (d *NotionDestination) createProperties(book domain.Book) map[string]notion.PropertyInput {
	mapping := d.notion.Mapping
	properties := map[string]notion.PropertyInput{
		mapping.Title:    notion.TitleInput(book.Title),
		mapping.KindleID: notion.RichTextInput(book.ID),
	}
	setRichText(properties, mapping.Author, book.Author)
	setRichText(properties, mapping.ASIN, book.ASIN)
	if mapping.LastAnnotatedAt != "" && book.LastAnnotatedAt != nil {
		properties[mapping.LastAnnotatedAt] = notion.DateInput(book.LastAnnotatedAt.UTC().Format(lastAnnotatedAtLayout))
	}
	return properties
}

// setRichText は mapping があり（propertyID が空でない）値もあるときだけ rich_text の Property を足す。
func setRichText(properties map[string]notion.PropertyInput, propertyID, text string) {
	if propertyID == "" || text == "" {
		return
	}
	properties[propertyID] = notion.RichTextInput(text)
}
