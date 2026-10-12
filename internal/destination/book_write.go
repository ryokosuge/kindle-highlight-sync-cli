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

// AdoptBook は remote の Kindle ID Property に book の Book ID を書き（§11）、§12 の Adopt 列に従って
// author / asin は index にある今の値が空のときだけ、last_annotated_at は Kindle の値があれば、同じ 1 回の更新で書く。
// title と mapping の無い Property は body に入れない（Notion は body に無い Property の値を残す）。
// そのページを book と exact で一致するものとして index に反映し、書いた author / asin も index の値にする。
// 呼び出し側は照合が adoptable の Book と、その Match が返したページにだけ使う前提（index に無いページは今の値をすべて空として扱う）。
// index をまだ作っていなければ先に作る。更新に失敗したら Notion の error（exitcode.Notion）をそのまま返し、index は変えない。
func (d *NotionDestination) AdoptBook(ctx context.Context, remote RemoteBook, book domain.Book) error {
	if err := d.ensureIndex(ctx); err != nil {
		return err
	}
	current, _ := d.index.Lookup(remote)
	values := PlanBookProperties(current, book, d.notion.Mapping)
	properties := propertyInputs(values)
	properties[d.notion.Mapping.KindleID] = notion.RichTextInput(book.ID)
	if err := d.client.UpdatePage(ctx, remote.ID, properties); err != nil {
		return err
	}
	d.index.Adopt(remote, book.ID)
	d.index.Update(remote, values)
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
		properties[mapping.LastAnnotatedAt] = dateInput(*book.LastAnnotatedAt)
	}
	return properties
}

// propertyInputs は PlanBookProperties の結果を Property ID を key にした更新の body にする。nil の Property は入れない。
// 値は空のときに Property を消す（rich_text の [] / date の null）ので、空の値は PlanBookProperties の側で nil にしてある前提。
// mapping の Property ID は項目ごとに異なる前提で書く。重複は config の読み込み側で弾く（別 Task で扱う）。
func propertyInputs(values BookPropertyValues) map[string]notion.PropertyInput {
	properties := make(map[string]notion.PropertyInput)
	if values.Author != nil {
		properties[values.Author.PropertyID] = notion.RichTextInput(values.Author.Value)
	}
	if values.ASIN != nil {
		properties[values.ASIN.PropertyID] = notion.RichTextInput(values.ASIN.Value)
	}
	if values.LastAnnotatedAt != nil {
		properties[values.LastAnnotatedAt.PropertyID] = dateInput(values.LastAnnotatedAt.Value)
	}
	return properties
}

// dateInput は日付だけ（lastAnnotatedAtLayout）の date Property の値を作る。
func dateInput(t time.Time) notion.PropertyInput {
	return notion.DateInput(t.UTC().Format(lastAnnotatedAtLayout))
}

// setRichText は mapping があり（propertyID が空でない）値もあるときだけ rich_text の Property を足す。
func setRichText(properties map[string]notion.PropertyInput, propertyID, text string) {
	if propertyID == "" || text == "" {
		return
	}
	properties[propertyID] = notion.RichTextInput(text)
}
