// api_audit_csv_test.go — end-to-end on /api/audit-log/export.csv.

package server

import (
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openweft/weft-webui/internal/audit"
)

func TestAuditCSV_ExportsRowsWithHeader(t *testing.T) {
	prev := auditTail
	t.Cleanup(func() { auditTail = prev })
	auditTail = &stubTailer{events: []audit.Event{
		{Timestamp: time.Date(2026, 6, 2, 12, 0, 1, 0, time.UTC),
			Action: "az.create", Subject: "alice", Result: "ok",
			Tenant: "acme", Project: "team-alpha", RemoteIP: "1.2.3.4"},
		{Timestamp: time.Date(2026, 6, 2, 12, 0, 2, 0, time.UTC),
			Action: "auth.callback.failed", Result: "error",
			ErrorMessage: "state_mismatch", RemoteIP: "9.9.9.9"},
	}}

	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL + "/api/audit-log/export.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", cd)
	}

	rows, err := csv.NewReader(resp.Body).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 { // header + 2 events
		t.Fatalf("rows = %d, want 3 (header + 2)", len(rows))
	}
	// Header sanity : action column must be present at the expected
	// position so downstream tooling (sheets, SIEM ingest) can rely
	// on the schema.
	header := rows[0]
	want := []string{
		"ts", "subject", "tenant", "project", "action",
		"resource_kind", "resource_id", "result", "error",
		"remote_ip", "request_id",
	}
	if len(header) != len(want) {
		t.Fatalf("header len = %d, want %d", len(header), len(want))
	}
	for i, w := range want {
		if header[i] != w {
			t.Errorf("header[%d] = %q, want %q", i, header[i], w)
		}
	}
	if rows[1][4] != "az.create" || rows[1][1] != "alice" || rows[1][9] != "1.2.3.4" {
		t.Errorf("row 1 unexpected : %+v", rows[1])
	}
	if rows[2][4] != "auth.callback.failed" || rows[2][8] != "state_mismatch" {
		t.Errorf("row 2 unexpected : %+v", rows[2])
	}
}

func TestAuditCSV_NoTailerReturns503(t *testing.T) {
	prev := auditTail
	t.Cleanup(func() { auditTail = prev })
	auditTail = nil

	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL + "/api/audit-log/export.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestAuditCSV_RejectsBadTimestamps(t *testing.T) {
	prev := auditTail
	t.Cleanup(func() { auditTail = prev })
	auditTail = &stubTailer{}

	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL + "/api/audit-log/export.csv?since=yesterday")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("bad since: status %d, want 400", resp.StatusCode)
	}
}

func TestAuditCSV_FilterByAction(t *testing.T) {
	prev := auditTail
	t.Cleanup(func() { auditTail = prev })
	auditTail = &stubTailer{events: []audit.Event{
		{Action: "az.create", Subject: "alice"},
		{Action: "rack.create", Subject: "bob"},
		{Action: "auth.callback.failed", Subject: "ip-x"},
	}}

	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL + "/api/audit-log/export.csv?action=auth.")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	rows, _ := csv.NewReader(resp.Body).ReadAll()
	if len(rows) != 2 { // header + 1 auth event
		t.Errorf("rows = %d, want 2 (header + 1)", len(rows))
	}
	if len(rows) >= 2 && rows[1][4] != "auth.callback.failed" {
		t.Errorf("row 1 = %q, want auth.callback.failed", rows[1][4])
	}
}
