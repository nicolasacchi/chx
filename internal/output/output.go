// Package output handles JSON / table rendering and gjson filtering.
//
// Convention (matches stx/sgx/jx):
//   - TTY default: table (when registered for the command)
//   - Pipe default: JSON
//   - --json: forces JSON on TTY
//   - --jq <gjson>: filters; applied only on the JSON path
//
// chx-specific notes:
//   - SQL responses come back in ClickHouse `FORMAT JSON` envelope: {meta, data, rows, statistics}
//     The renderer reads `data` as the rows array (overridable per-command via commandRowsPath).
//   - Cloud Mgmt API responses are usually a top-level array (or a single object); set
//     commandRowsPath["<key>"] = "@this" for those.
package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/tidwall/gjson"
)

// PrintData writes data to stdout. Behaviour:
//   - TTY + command has a column map + !jsonMode + no jqFilter → table render
//   - otherwise → JSON (with optional gjson --jq filter)
//
// The command key is "<group>.<leaf>" (e.g. "databases.list", "services.list").
func PrintData(command string, data []byte, jsonMode bool, jqFilter string) error {
	useTable := !jsonMode && jqFilter == "" && IsTTY()
	if useTable {
		err := printTable(command, data)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errNoTable) {
			return err
		}
		// errNoTable: fall through to JSON
	}

	if jqFilter != "" {
		filtered, err := ApplyFilter(data, jqFilter)
		if err != nil {
			return err
		}
		data = filtered
	}
	return printJSON(os.Stdout, data)
}

// ApplyFilter runs a gjson expression against data.
// gjson syntax — NOT real jq. Examples:
//
//	"data.0.name"           first element's name
//	"data.#.name"           all names (array)
//	"data.#"                row count
//	"#.id"                  for top-level array responses (Cloud API list endpoints)
func ApplyFilter(data []byte, expr string) ([]byte, error) {
	res := gjson.GetBytes(data, expr)
	if !res.Exists() {
		return []byte("null"), nil
	}
	return []byte(res.Raw), nil
}

// PrintRaw writes data verbatim followed by a newline. Used by --ndjson and `chx api`
// passthrough where we want zero re-encoding.
func PrintRaw(data []byte) error {
	_, err := os.Stdout.Write(append(data, '\n'))
	return err
}

func printJSON(w io.Writer, data []byte) error {
	if isTTY(w) {
		var pretty buffer
		if err := jsonIndent(&pretty, data); err == nil {
			_, err := fmt.Fprintln(w, pretty.String())
			return err
		}
	}
	_, err := w.Write(append(data, '\n'))
	return err
}

// IsTTY returns true when stdout is a terminal (pipes/redirects -> false).
func IsTTY() bool { return isTTY(os.Stdout) }

func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

// --- thin wrappers to avoid an extra import in the test surface ---

type buffer struct{ buf []byte }

func (b *buffer) Write(p []byte) (int, error) { b.buf = append(b.buf, p...); return len(p), nil }
func (b *buffer) String() string              { return string(b.buf) }

func jsonIndent(dst *buffer, src []byte) error {
	var v any
	if err := json.Unmarshal(src, &v); err != nil {
		return err
	}
	enc := json.NewEncoder(dst)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
