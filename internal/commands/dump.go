package commands

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tidwall/gjson"

	"github.com/nicolasacchi/chx/internal/client"
)

var (
	dumpDatabase   string
	dumpTablesLike string
	dumpOutput     string
	dumpFormat     string
	dumpCompress   string
	dumpSchemaOnly bool
)

var dumpCmd = &cobra.Command{
	Use:   "dump",
	Short: "Export a database (DDL + per-table data) to a local directory",
	Long: `Dump every table in a database to a local directory as a logical export.

Output layout:
    <output>/
        manifest.json                  per-export metadata (server, exported_at, tables)
        <db>/<table>.sql               DDL from SHOW CREATE TABLE
        <db>/<table>.<ext>[.gz]        data in --format (Native | Parquet | JSONEachRow | TSV)

Streaming: data is piped from the SQL endpoint straight to disk (no in-memory
buffering) via SQLClient.Stream, so multi-GB tables don't OOM chx.

Important caveat about ClickHouse Cloud backups
-----------------------------------------------
This is a LOGICAL export. ClickHouse Cloud does not expose binary backup
contents for download — only restore-into-a-new-service (see
'chx services backups restore'). 'chx dump' is the right tool when you want
a portable copy of the data; it's not a substitute for Cloud-managed backups
when you need point-in-time consistency at the storage layer.

Examples:
    chx dump --database analytics --output ./backup-$(date +%F)
    chx dump --database analytics --tables 'events_%' --format Parquet --compress gzip --output ./events-dump
    chx dump --database analytics --schema-only --output ./schema`,
	Args: cobra.NoArgs,
	RunE: runDump,
}

func init() {
	dumpCmd.Flags().StringVar(&dumpDatabase, "db", "", "Database to dump (required; falls back to the global --database)")
	dumpCmd.Flags().StringVar(&dumpTablesLike, "tables", "", "Optional LIKE pattern for table names (e.g. 'events_%')")
	dumpCmd.Flags().StringVar(&dumpOutput, "output", "", "Output directory (required, will be created if missing)")
	dumpCmd.Flags().StringVar(&dumpFormat, "format", "Native", "Wire format for data files: Native, Parquet, JSONEachRow, TSV, CSV")
	dumpCmd.Flags().StringVar(&dumpCompress, "compress", "none", "Compress data files: none or gzip")
	dumpCmd.Flags().BoolVar(&dumpSchemaOnly, "schema-only", false, "Write only DDL (no data files, no row counts)")
	rootCmd.AddCommand(dumpCmd)
}

// tableManifestEntry is one row in manifest.json's tables[] array.
type tableManifestEntry struct {
	Database  string `json:"database"`
	Name      string `json:"name"`
	Engine    string `json:"engine"`
	TotalRows int64  `json:"total_rows"`
	Format    string `json:"format,omitempty"`
	DDLFile   string `json:"ddl_file"`
	DataFile  string `json:"data_file,omitempty"`
	Bytes     int64  `json:"bytes_written,omitempty"`
	Skipped   string `json:"skipped,omitempty"`
}

type manifest struct {
	Database   string               `json:"database"`
	ExportedAt string               `json:"exported_at"`
	ChxVersion string               `json:"chx_version,omitempty"`
	Server     string               `json:"server,omitempty"`
	Format     string               `json:"format"`
	Compress   string               `json:"compress"`
	SchemaOnly bool                 `json:"schema_only"`
	Tables     []tableManifestEntry `json:"tables"`
}

func runDump(cmd *cobra.Command, args []string) error {
	db := dumpDatabase
	if db == "" {
		db = databaseFlag
	}
	if db == "" {
		return fmt.Errorf("--db is required (or set the global --database)")
	}
	if dumpOutput == "" {
		return fmt.Errorf("--output is required")
	}
	if dumpCompress != "none" && dumpCompress != "gzip" {
		return fmt.Errorf("--compress must be 'none' or 'gzip' (got %q)", dumpCompress)
	}

	c, _, err := getSQLClient()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Join(dumpOutput, db), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	tables, err := listDumpTables(c, db, dumpTablesLike)
	if err != nil {
		return err
	}
	if len(tables) == 0 {
		return fmt.Errorf("no tables matched in database %q (pattern %q)", db, dumpTablesLike)
	}

	fmt.Fprintf(os.Stderr, "chx dump: %d table(s) to export to %s\n", len(tables), dumpOutput)

	mf := manifest{
		Database:   db,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		ChxVersion: rootCmd.Version,
		Server:     c.BaseURL(),
		Format:     dumpFormat,
		Compress:   dumpCompress,
		SchemaOnly: dumpSchemaOnly,
	}

	for _, t := range tables {
		entry := tableManifestEntry{
			Database:  t.database,
			Name:      t.name,
			Engine:    t.engine,
			TotalRows: t.totalRows,
		}

		ddlPath, derr := dumpTableDDL(c, t, dumpOutput)
		if derr != nil {
			return fmt.Errorf("dump DDL for %s.%s: %w", t.database, t.name, derr)
		}
		entry.DDLFile = relTo(dumpOutput, ddlPath)

		if dumpSchemaOnly {
			entry.Skipped = "schema_only"
			mf.Tables = append(mf.Tables, entry)
			fmt.Fprintf(os.Stderr, "  %s.%s: DDL ✓ (schema-only)\n", t.database, t.name)
			continue
		}

		// Refuse to dump non-data engines (Distributed, View, MaterializedView) —
		// their data is logical, dumping SELECT * either fails or duplicates upstream.
		if !isDataEngine(t.engine) {
			entry.Skipped = "non-data engine " + t.engine
			mf.Tables = append(mf.Tables, entry)
			fmt.Fprintf(os.Stderr, "  %s.%s: DDL ✓, data skipped (engine=%s)\n", t.database, t.name, t.engine)
			continue
		}

		dataPath, n, derr := dumpTableData(c, t, dumpOutput)
		if derr != nil {
			return fmt.Errorf("dump data for %s.%s: %w", t.database, t.name, derr)
		}
		entry.DataFile = relTo(dumpOutput, dataPath)
		entry.Format = dumpFormat
		entry.Bytes = n
		mf.Tables = append(mf.Tables, entry)
		fmt.Fprintf(os.Stderr, "  %s.%s: DDL ✓, data %s (%s)\n",
			t.database, t.name, humanBytes(n), dumpFormat)
	}

	mfPath := filepath.Join(dumpOutput, "manifest.json")
	mfData, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := os.WriteFile(mfPath, append(mfData, '\n'), 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	fmt.Fprintf(os.Stderr, "chx dump: wrote manifest → %s\n", mfPath)
	return nil
}

// dumpTableInfo carries the minimal table metadata needed per-table.
type dumpTableInfo struct {
	database  string
	name      string
	engine    string
	totalRows int64
}

// listDumpTables queries system.tables, returning the rows we care about.
func listDumpTables(c *client.SQLClient, db, like string) ([]dumpTableInfo, error) {
	conds := []string{"database = " + sqlString(db)}
	if like != "" {
		conds = append(conds, "name LIKE "+sqlString(like))
	}
	sql := `
SELECT database, name, engine, total_rows
FROM system.tables
WHERE ` + strings.Join(conds, " AND ") + `
ORDER BY name
`
	res, err := c.Query(ctx(), sql, client.SQLOptions{
		Format:        "JSON",
		MaxResultRows: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	rows := gjson.GetBytes(res.Body, "data").Array()
	out := make([]dumpTableInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, dumpTableInfo{
			database:  r.Get("database").String(),
			name:      r.Get("name").String(),
			engine:    r.Get("engine").String(),
			totalRows: r.Get("total_rows").Int(),
		})
	}
	return out, nil
}

// dumpTableDDL writes SHOW CREATE TABLE output to <output>/<db>/<name>.sql.
func dumpTableDDL(c *client.SQLClient, t dumpTableInfo, outRoot string) (string, error) {
	sql := fmt.Sprintf("SHOW CREATE TABLE `%s`.`%s` FORMAT TabSeparatedRaw",
		strings.ReplaceAll(t.database, "`", "``"),
		strings.ReplaceAll(t.name, "`", "``"))
	res, err := c.Query(ctx(), sql, client.SQLOptions{
		Format:        "TabSeparatedRaw",
		MaxResultRows: 0,
	})
	if err != nil {
		return "", err
	}
	path := filepath.Join(outRoot, t.database, t.name+".sql")
	// Append a trailing newline if absent; ClickHouse omits it on TabSeparatedRaw.
	body := res.Body
	if len(body) > 0 && !bytes.HasSuffix(body, []byte("\n")) {
		body = append(body, '\n')
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// dumpTableData streams SELECT * FROM table FORMAT <fmt> into a file.
// Returns the file path and the number of bytes written to disk
// (post-compression if --compress=gzip).
func dumpTableData(c *client.SQLClient, t dumpTableInfo, outRoot string) (string, int64, error) {
	ext := dataExtension(dumpFormat)
	fname := t.name + ext
	if dumpCompress == "gzip" {
		fname += ".gz"
	}
	path := filepath.Join(outRoot, t.database, fname)

	f, err := os.Create(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	counter := &countingWriter{w: f}
	var dst io.Writer = counter
	var gz *gzip.Writer
	if dumpCompress == "gzip" {
		gz = gzip.NewWriter(counter)
		dst = gz
	}

	sql := fmt.Sprintf("SELECT * FROM `%s`.`%s` FORMAT %s",
		strings.ReplaceAll(t.database, "`", "``"),
		strings.ReplaceAll(t.name, "`", "``"),
		dumpFormat)
	_, err = c.Stream(ctx(), sql, client.SQLOptions{
		Format:        dumpFormat,
		MaxResultRows: 0, // 0 = no cap; dumps need everything
	}, dst)
	if gz != nil {
		if cerr := gz.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("gzip close: %w", cerr)
		}
	}
	if err != nil {
		return "", counter.n, err
	}
	return path, counter.n, nil
}

// countingWriter wraps an io.Writer to track total bytes written.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// dataExtension maps a ClickHouse wire format to a sensible file extension.
func dataExtension(format string) string {
	switch format {
	case "Native":
		return ".native"
	case "Parquet":
		return ".parquet"
	case "JSONEachRow":
		return ".jsonl"
	case "TSV", "TabSeparated":
		return ".tsv"
	case "CSV":
		return ".csv"
	case "TabSeparatedRaw":
		return ".tsv"
	default:
		return ".dat"
	}
}

// isDataEngine returns true when SELECT * FROM <engine> meaningfully exports table data.
// Distributed/View/Materialized views read upstream tables — exporting them
// duplicates data the upstream tables already own and may fail on shard fan-out.
func isDataEngine(engine string) bool {
	switch engine {
	case "Distributed", "View", "MaterializedView", "LiveView", "WindowView", "Merge", "Null", "Dictionary":
		return false
	}
	return true
}

// relTo returns the path relative to root, or path if filepath.Rel fails.
func relTo(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return rel
}

// humanBytes renders a byte count with a binary unit, e.g. 1.2 MiB.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
