package commands

import "testing"

func TestDataExtension(t *testing.T) {
	cases := map[string]string{
		"Native":      ".native",
		"Parquet":     ".parquet",
		"JSONEachRow": ".jsonl",
		"TSV":         ".tsv",
		"CSV":         ".csv",
		"Weird":       ".dat",
	}
	for in, want := range cases {
		if got := dataExtension(in); got != want {
			t.Errorf("dataExtension(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsDataEngine(t *testing.T) {
	skip := []string{"Distributed", "View", "MaterializedView", "Merge", "Null", "Dictionary"}
	for _, e := range skip {
		if isDataEngine(e) {
			t.Errorf("isDataEngine(%q): expected false", e)
		}
	}
	keep := []string{"MergeTree", "ReplicatedMergeTree", "Log", "TinyLog", "ReplacingMergeTree"}
	for _, e := range keep {
		if !isDataEngine(e) {
			t.Errorf("isDataEngine(%q): expected true", e)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:                  "0 B",
		512:                "512 B",
		1024:               "1.0 KiB",
		1536:               "1.5 KiB",
		1024 * 1024:        "1.0 MiB",
		1024 * 1024 * 1024: "1.0 GiB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
