package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ThePun1sh3R/costhound/internal/checks"
	"github.com/ThePun1sh3R/costhound/internal/license"
)

func TestChecksListFreeAWS(t *testing.T) {
	rows := awsCheckRows(true)
	if len(rows) != checks.Count(license.PlanFree) {
		t.Fatalf("free rows %d, want %d", len(rows), checks.Count(license.PlanFree))
	}
	seen := map[string]int{}
	for _, r := range rows {
		if r.Cloud != "aws" || r.Plan != license.PlanFree {
			t.Errorf("unexpected row: %+v", r)
		}
		seen[r.ID]++
		if r.Finds == "" || r.Pricing == "" {
			t.Errorf("%s: empty finds/pricing", r.ID)
		}
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("%s appears %d times", id, n)
		}
	}
}

func TestChecksListJSONShape(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := checksListTo([]string{"-cloud", "aws", "-plan", "free", "-json"}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, errBuf.String())
	}
	var rows []checkRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if len(rows) == 0 {
		t.Fatal("empty")
	}
	for _, r := range rows {
		if r.Cloud == "" || r.ID == "" || r.Plan == "" || r.Finds == "" || r.Pricing == "" {
			t.Fatalf("incomplete row: %+v", r)
		}
	}
}

func TestChecksListMarkdown(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := checksListTo([]string{"-cloud", "aws", "-plan", "free", "-markdown"}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, errBuf.String())
	}
	s := out.String()
	if !strings.HasPrefix(s, "| Cloud | ID | Plan | What it finds | Pricing | Fix script |") {
		t.Fatalf("header: %q", s)
	}
	if !strings.Contains(s, "| aws | `unattached-ebs` |") {
		t.Fatalf("missing unattached-ebs row:\n%s", s)
	}
}

func TestChecksListRejectsBadFlags(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := checksListTo([]string{"-cloud", "oracle"}, &out, &errBuf); code != 2 {
		t.Fatalf("bad cloud: exit %d", code)
	}
	errBuf.Reset()
	if code := checksListTo([]string{"-json", "-markdown"}, &out, &errBuf); code != 2 {
		t.Fatalf("json+markdown: exit %d", code)
	}
}
