package notion

import "errors"

const (
	statusNotFound     = 404
	codeObjectNotFound = "object_not_found"
)

// IsNotShared は err が、integration が対象に接続されていない可能性のある Notion の失敗かを返す。
// 404 の object_not_found は対象が存在しない場合と区別できないため、true は未接続の可能性を示すだけである。
func IsNotShared(err error) bool {
	var se *StatusError
	if !errors.As(err, &se) {
		return false
	}
	return se.Status == statusNotFound && se.Code == codeObjectNotFound
}
