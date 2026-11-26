package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestResolveBuyAccountsSTS(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_CONFIG_FILE", home+"/no-config")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", home+"/no-creds")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_DEFAULT_PROFILE", "")

	var sawSTS, sawOrg bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s := string(body) + r.URL.RawQuery + r.Header.Get("X-Amz-Target")
		switch {
		case strings.Contains(s, "GetCallerIdentity") || strings.Contains(r.URL.Path, "GetCallerIdentity"):
			sawSTS = true
			w.Header().Set("Content-Type", "text/xml")
			_, _ = io.WriteString(w, `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <GetCallerIdentityResult>
    <Arn>arn:aws:iam::111122223333:user/fixture</Arn>
    <UserId>AIDAFIXTURE</UserId>
    <Account>111122223333</Account>
  </GetCallerIdentityResult>
</GetCallerIdentityResponse>`)
		case strings.Contains(s, "ListAccounts") || strings.Contains(r.Header.Get("X-Amz-Target"), "ListAccounts"):
			sawOrg = true
			w.Header().Set("Content-Type", "application/x-amz-json-1.1")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Accounts": []map[string]any{
					{"Id": "222222222222", "Status": "ACTIVE", "Name": "b"},
					{"Id": "111122223333", "Status": "ACTIVE", "Name": "a"},
					{"Id": "111122223333", "Status": "ACTIVE", "Name": "a-dup"},
					{"Id": "333333333333", "Status": "SUSPENDED", "Name": "gone"},
				},
			})
		default:
			// STS query protocol posts Action=GetCallerIdentity.
			if r.Method == http.MethodPost && strings.Contains(string(body), "Action=GetCallerIdentity") {
				sawSTS = true
				w.Header().Set("Content-Type", "text/xml")
				_, _ = io.WriteString(w, `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <GetCallerIdentityResult>
    <Account>111122223333</Account>
    <Arn>arn:aws:iam::111122223333:user/fixture</Arn>
    <UserId>AIDAFIXTURE</UserId>
  </GetCallerIdentityResult>
</GetCallerIdentityResponse>`)
				return
			}
			http.Error(w, "unexpected "+r.URL.String()+" "+string(body), 500)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("AWS_ENDPOINT_URL", srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ids, err := resolveBuyAccounts(ctx, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !sawSTS || len(ids) != 1 || ids[0] != "111122223333" {
		t.Fatalf("self: ids=%v sawSTS=%v err path", ids, sawSTS)
	}

	ids, err = resolveBuyAccounts(ctx, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !sawOrg {
		t.Fatal("org mode must call ListAccounts")
	}
	// Active only, sorted; duplicate ID from the API still appears once per list entry
	// unless the API dedupes — we keep each active Id as returned then sort.
	if len(ids) < 2 || ids[0] != "111122223333" {
		t.Fatalf("org accounts = %v", ids)
	}
	for _, id := range ids {
		if id == "333333333333" {
			t.Fatalf("suspended account must be skipped: %v", ids)
		}
		if len(id) != 12 {
			t.Fatalf("invalid id slipped through: %q", id)
		}
	}
}
