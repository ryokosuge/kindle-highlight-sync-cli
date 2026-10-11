package destination

import (
	"context"
	"fmt"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/domain"
)

// UpdateBook は exact で一致した remote のページに §12 の Subsequent Sync 列で書く:
// author / asin は index にある今の値が空のときだけ、last_annotated_at は Kindle の値があれば、1 回の更新で書く。
// title と Kindle ID Property、mapping の無い Property は body に入れない（Notion は body に無い Property の値を残す）。
// 書く Property が 1 つも無ければ Notion に要求を送らずに成功する。
// index をまだ作っていなければ先に作る。index に remote のページが無ければ要求を送らずに error を返す。
// 更新に失敗したら Notion の error（exitcode.Notion）をそのまま返し、index は変えない。成功したら書いた author / asin を index の値にする。
func (d *NotionDestination) UpdateBook(ctx context.Context, remote RemoteBook, book domain.Book) error {
	if err := d.ensureIndex(ctx); err != nil {
		return err
	}
	current, ok := d.index.Lookup(remote)
	if !ok {
		return fmt.Errorf("notion page %s is not in the book index", remote.ID)
	}
	values := PlanBookProperties(current, book, d.notion.Mapping)
	if values.IsEmpty() {
		return nil
	}
	if err := d.client.UpdatePage(ctx, remote.ID, propertyInputs(values)); err != nil {
		return err
	}
	d.index.Update(remote, values)
	return nil
}
