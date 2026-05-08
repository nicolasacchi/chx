package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/tidwall/gjson"
)

// ColumnDef defines one table column for one command.
type ColumnDef struct {
	Header string
	Key    string // gjson path relative to one row in the rows array
	Format FormatFunc
}

// commandColumns maps "<group>.<leaf>" command keys to column layouts.
//
// Phase 0 ships with an empty registry; Phase 1+ adds entries as commands land.
// Commands without an entry fall through to JSON output automatically.
var commandColumns = map[string][]ColumnDef{}

// commandRowsPath overrides the gjson path used to extract the rows array.
// Default for SQL responses: "data" (the JSON envelope's data array).
// For Cloud API list responses (top-level arrays), set to "@this".
var commandRowsPath = map[string]string{}

// Register lets command files self-register their column layout.
// Call from the command file's init() so table renderings stay co-located with the command.
func Register(command string, cols []ColumnDef, rowsPath string) {
	commandColumns[command] = cols
	if rowsPath != "" {
		commandRowsPath[command] = rowsPath
	}
}

var errNoTable = errors.New("no table layout")

func printTable(command string, data []byte) error {
	cols, ok := commandColumns[command]
	if !ok {
		return errNoTable
	}

	rowsPath := commandRowsPath[command]
	if rowsPath == "" {
		rowsPath = "data" // ClickHouse FORMAT JSON envelope default
	}

	rows := gjson.GetBytes(data, rowsPath)
	if !rows.Exists() {
		return fmt.Errorf("no rows at %q", rowsPath)
	}

	tw := table.NewWriter()
	tw.SetOutputMirror(os.Stdout)
	tw.SetStyle(table.StyleLight)
	tw.Style().Format.Header = text.FormatUpper

	headers := make(table.Row, len(cols))
	for i, c := range cols {
		headers[i] = c.Header
	}
	tw.AppendHeader(headers)

	rendered := 0
	rows.ForEach(func(_, row gjson.Result) bool {
		out := make(table.Row, len(cols))
		for i, c := range cols {
			cell := row.Get(c.Key)
			var v any
			if !cell.Exists() {
				v = ""
			} else {
				v = unwrap(cell)
			}
			if c.Format != nil {
				out[i] = c.Format(v)
			} else {
				out[i] = v
			}
		}
		tw.AppendRow(out)
		rendered++
		return true
	})

	if rendered == 0 {
		fmt.Println("(no rows)")
		return nil
	}

	tw.Render()
	return nil
}

func unwrap(r gjson.Result) any {
	switch r.Type {
	case gjson.Number:
		return r.Num
	case gjson.String:
		return r.Str
	case gjson.True:
		return true
	case gjson.False:
		return false
	case gjson.Null:
		return nil
	default:
		var v any
		_ = json.Unmarshal([]byte(r.Raw), &v)
		return v
	}
}
