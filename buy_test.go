package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBuyLink(t *testing.T) {
	got := buyLink("pro", "month", []string{"123456789012"})
	want := "https://costhound.dev/#buy=pro,month,123456789012"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	got = buyLink("team", "year", []string{"111111111111", "222222222222"})
	want = "https://costhound.dev/#buy=team,year,111111111111+222222222222"
	if got != want {
		t.Errorf("multiple accounts: got %q, want %q", got, want)
	}
}

func TestBuyDefaultsAndFlags(t *testing.T) {
	lookup := func(_ context.Context, profile string, org bool) ([]string, error) {
		if profile != "" || org {
			t.Errorf("defaults should not set profile=%q org=%v", profile, org)
		}
		return []string{"123456789012"}, nil
	}
	var out, errb bytes.Buffer
	if code := buyTo(nil, &out, &errb, lookup); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	s := out.String()
	if !strings.Contains(s, "https://costhound.dev/#buy=pro,month,123456789012") {
		t.Errorf("defaults: %s", s)
	}
	if !strings.Contains(s, "URL fragment") || !strings.Contains(s, "never send") {
		t.Errorf("missing fragment note: %s", s)
	}
	if errb.Len() != 0 {
		t.Errorf("stderr: %s", errb.String())
	}

	lookup = func(_ context.Context, profile string, org bool) ([]string, error) {
		if profile != "pay" || !org {
			t.Errorf("got profile=%q org=%v", profile, org)
		}
		return []string{"111111111111", "222222222222"}, nil
	}
	out.Reset()
	errb.Reset()
	if code := buyTo([]string{"-plan", "team", "-term", "year", "-profile", "pay", "-org"}, &out, &errb, lookup); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "https://costhound.dev/#buy=team,year,111111111111+222222222222") {
		t.Errorf("team/org: %s", out.String())
	}
}

func TestBuyBadPlanTerm(t *testing.T) {
	lookup := func(context.Context, string, bool) ([]string, error) {
		t.Fatal("lookup must not run for a bad plan/term")
		return nil, nil
	}
	var out, errb bytes.Buffer
	if code := buyTo([]string{"-plan", "enterprise"}, &out, &errb, lookup); code != 2 || !strings.Contains(errb.String(), `-plan is pro or team, not "enterprise"`) {
		t.Errorf("bad plan: %d %q %q", code, out.String(), errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := buyTo([]string{"-term", "week"}, &out, &errb, lookup); code != 2 || !strings.Contains(errb.String(), `-term is month or year, not "week"`) {
		t.Errorf("bad term: %d %q %q", code, out.String(), errb.String())
	}
}

func TestBuyNoCredentialsFallback(t *testing.T) {
	lookup := func(context.Context, string, bool) ([]string, error) {
		return nil, errors.New("no AWS credentials found (fake)")
	}
	var out, errb bytes.Buffer
	if code := buyTo(nil, &out, &errb, lookup); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.TrimSpace(out.String()) != "https://costhound.dev" {
		t.Errorf("stdout want plain site link, got %q", out.String())
	}
	if !strings.Contains(errb.String(), "no AWS credentials found (fake)") {
		t.Errorf("stderr: %s", errb.String())
	}
	if strings.Contains(out.String(), "#buy=") {
		t.Errorf("fallback must not include #buy=: %s", out.String())
	}
}
