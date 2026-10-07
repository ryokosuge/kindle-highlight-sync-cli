// Package config loads config.toml.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

type Config struct {
	Amazon Amazon `toml:"amazon"`
	Notion Notion `toml:"notion"`
}

type Amazon struct {
	Region string `toml:"region"`
}

type Notion struct {
	DatabaseID   string  `toml:"database_id"`
	DataSourceID string  `toml:"data_source_id"`
	Mapping      Mapping `toml:"mapping"`
}

type Mapping struct {
	Title           string `toml:"title"`
	Author          string `toml:"author"`
	ASIN            string `toml:"asin"`
	KindleID        string `toml:"kindle_id"`
	LastAnnotatedAt string `toml:"last_annotated_at"`
}

// knownKeys は toml タグから作る、受け付ける key の完全な path（綴りは完全一致）。
// go-toml は key を大文字小文字を区別せずに照合するので、綴りの違う key はこれで弾く。
var knownKeys = keyPaths(reflect.TypeFor[Config](), "")

// Load は path の config.toml を読む。ファイルが無ければ fs.ErrNotExist を包んだ error を返す。
// TOML の誤り・型違い・未知の key は、path と位置（行・列 / key）を含む error にする。
// error の文言は path・key・位置だけで作り、config.toml の値や decoder の文言を含めない。
// decoder の error（*toml.DecodeError / *toml.StrictMissingError）は errors.As で取り出せる。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, decodeError(path, data, err)
	}
	if err := checkKeys(path, data); err != nil {
		return nil, err
	}
	return &cfg, nil
}

type loadError struct {
	msg string
	err error
}

func (e *loadError) Error() string { return e.msg }

func (e *loadError) Unwrap() error { return e.err }

func decodeError(path string, data []byte, err error) error {
	ents, _ := entries(data)

	var missing *toml.StrictMissingError
	if errors.As(err, &missing) {
		keys := make([]string, len(missing.Errors))
		for i := range missing.Errors {
			keys[i] = describe(&missing.Errors[i], ents)
		}
		return &loadError{msg: fmt.Sprintf("%s: unknown key %s", path, strings.Join(keys, ", ")), err: err}
	}

	var de *toml.DecodeError
	if errors.As(err, &de) {
		if d, ok := duplicate(de, ents); ok {
			return &loadError{msg: fmt.Sprintf("%s: duplicate key %s (line %d, column %d)", path, strings.Join(d.path, "."), d.key.Start.Line, d.key.Start.Column), err: err}
		}
		if len(fullKey(de, ents)) == 0 {
			return &loadError{msg: fmt.Sprintf("%s: invalid TOML (%s)", path, position(de)), err: err}
		}
		return &loadError{msg: fmt.Sprintf("%s: invalid key %s", path, describe(de, ents)), err: err}
	}

	return &loadError{msg: path + ": invalid config", err: err}
}

func checkKeys(path string, data []byte) error {
	ents, err := entries(data)
	if err != nil {
		return &loadError{msg: path + ": invalid TOML", err: err}
	}
	for _, e := range ents {
		for i := range e.path {
			key := strings.Join(e.path[:i+1], ".")
			if !knownKeys[key] {
				return &loadError{msg: fmt.Sprintf("%s: unknown key %s (line %d, column %d)", path, key, e.key.Start.Line, e.key.Start.Column)}
			}
		}
	}
	return nil
}

// duplicate は、DecodeError の位置の式に含まれ、同じ完全な path が 2 回目に現れる entry を返す。
// inline table の中の重複では DecodeError の位置も Key() も外側の key を指すため。
// 位置の式に含まれない重複は decoder が止まった error と無関係なので返さない。
func duplicate(de *toml.DecodeError, ents []entry) (entry, bool) {
	row, col := de.Position()
	seen := map[string]bool{}
	for _, d := range ents {
		key := strings.Join(d.path, ".")
		if seen[key] && atError(d, ents, row, col) {
			return d, true
		}
		seen[key] = true
	}
	return entry{}, false
}

// atError は d が (row, col) を含む entry 自身か、(row, col) を含む entry の value の中にあるかを返す。
func atError(d entry, ents []entry, row, col int) bool {
	at := func(e entry) bool { return contains(e.key, row, col) || contains(e.value, row, col) }
	if at(d) {
		return true
	}
	for _, e := range ents {
		if at(e) && contains(e.value, d.key.Start.Line, d.key.Start.Column) {
			return true
		}
	}
	return false
}

func describe(de *toml.DecodeError, ents []entry) string {
	key := fullKey(de, ents)
	if len(key) == 0 {
		return position(de)
	}
	return fmt.Sprintf("%s (%s)", strings.Join(key, "."), position(de))
}

func position(de *toml.DecodeError) string {
	row, col := de.Position()
	return fmt.Sprintf("line %d, column %d", row, col)
}

// fullKey は error の位置を含む最も内側の key の完全な path を返す。
// inline table の中の誤りでは DecodeError.Key() が外側の key までしか持たないため。
func fullKey(de *toml.DecodeError, ents []entry) []string {
	row, col := de.Position()
	key := []string(de.Key())
	for _, e := range ents {
		if contains(e.key, row, col) || contains(e.value, row, col) {
			key = e.path
		}
	}
	return key
}

func contains(s unstable.Shape, row, col int) bool {
	after := func(p unstable.Position) bool { return row > p.Line || row == p.Line && col >= p.Column }
	return after(s.Start) && !after(s.End)
}

// entry は config.toml に書かれた table header か key-value 1 つ。
// path は table header・dotted key・inline table を展開した完全な path。
type entry struct {
	path  []string
	key   unstable.Shape
	value unstable.Shape
}

func entries(data []byte) ([]entry, error) {
	var p unstable.Parser
	p.Reset(data)

	var table []string
	var out []entry
	for p.NextExpression() {
		e := p.Expression()
		switch e.Kind {
		case unstable.Table, unstable.ArrayTable:
			var keyShape unstable.Shape
			table, keyShape = keyParts(&p, nil, e.Key())
			out = append(out, entry{path: table, key: keyShape})
		case unstable.KeyValue:
			out = appendKeyValue(&p, out, table, e)
		}
	}
	return out, p.Error()
}

func appendKeyValue(p *unstable.Parser, out []entry, prefix []string, kv *unstable.Node) []entry {
	path, keyShape := keyParts(p, prefix, kv.Key())
	value := kv.Value()
	i := len(out)
	out = append(out, entry{path: path, key: keyShape, value: p.Shape(value.Raw)})
	if value.Kind == unstable.InlineTable {
		children := value.Children()
		for children.Next() {
			out = appendKeyValue(p, out, path, children.Node())
		}
		// inline table の value の範囲は "{" だけなので、中の最後の entry の終わりまで広げる。
		if len(out) > i+1 {
			out[i].value.End = out[len(out)-1].value.End
		}
	}
	return out
}

func keyParts(p *unstable.Parser, prefix []string, it unstable.Iterator) ([]string, unstable.Shape) {
	path := append([]string(nil), prefix...)
	var shape unstable.Shape
	for it.Next() {
		n := it.Node()
		if len(path) == len(prefix) {
			shape = p.Shape(n.Raw)
		}
		path = append(path, string(n.Data))
	}
	return path, shape
}

func keyPaths(t reflect.Type, prefix string) map[string]bool {
	keys := map[string]bool{}
	for f := range t.Fields() {
		key := prefix + f.Tag.Get("toml")
		keys[key] = true
		if f.Type.Kind() == reflect.Struct {
			for k := range keyPaths(f.Type, key+".") {
				keys[k] = true
			}
		}
	}
	return keys
}
