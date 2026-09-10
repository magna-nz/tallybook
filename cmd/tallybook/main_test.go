package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusNoIngest(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tallybook.db")

	root := newRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--db", dbPath, "--no-ingest", "status"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\noutput:\n%s", err, out.String())
	}

	got := out.String()
	if !strings.Contains(got, dbPath) {
		t.Errorf("status output missing db path %q:\n%s", dbPath, got)
	}
	if !strings.Contains(got, "skipped (--no-ingest)") {
		t.Errorf("status output missing skipped-ingest note:\n%s", got)
	}
}

func TestPricesJSON(t *testing.T) {
	root := newRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"prices", "--json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\noutput:\n%s", err, out.String())
	}

	var doc struct {
		Schema int `json:"schema"`
		Models []struct {
			ID    string  `json:"id"`
			Input float64 `json:"input"`
		} `json:"models"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("prices --json did not parse: %v\noutput:\n%s", err, out.String())
	}
	if doc.Schema != 1 {
		t.Errorf("schema = %d, want 1", doc.Schema)
	}
	if len(doc.Models) == 0 {
		t.Error("prices --json returned no models")
	}
}

func TestFindingUsageErrorExitsCleanly(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tallybook.db")

	root := newRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--db", dbPath, "--no-ingest", "finding", "not-a-number"})

	err := root.Execute()
	if err == nil {
		t.Fatal("Execute with a non-numeric finding index returned nil error, want one")
	}
	var ue usageError
	if !errorsAsUsageError(err, &ue) {
		t.Errorf("finding with bad index did not produce a usageError: %v (%T)", err, err)
	}
}

// errorsAsUsageError is a tiny local wrapper so the test file doesn't need
// its own "errors" import alongside the one in main.go's build.
func errorsAsUsageError(err error, target *usageError) bool {
	for err != nil {
		if ue, ok := err.(usageError); ok {
			*target = ue
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
