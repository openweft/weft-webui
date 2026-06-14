// resources_csv_test.go — pin /api/resources/{id}/export.csv +
// the inline `?format=csv` switch on writePage.

package server

import (
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResourceCSV_ExportsRowsWithHeader(t *testing.T) {
	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL + "/api/resources/azs/export.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", cd)
	}
	if !strings.Contains(resp.Header.Get("Content-Disposition"), "azs-") {
		t.Errorf("filename should embed the resource id, got %q", resp.Header.Get("Content-Disposition"))
	}

	rows, err := csv.NewReader(resp.Body).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 2 {
		t.Fatalf("rows = %d, want >= 2 (header + at least 1 row)", len(rows))
	}
	// Header order must match Resource.Columns. azs registry has
	// Code first (per resources.go).
	if rows[0][0] != "Code" {
		t.Errorf("first column = %q, want Code", rows[0][0])
	}
}

func TestResourceCSV_UnknownResource404(t *testing.T) {
	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL + "/api/resources/not-a-thing/export.csv")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestResourceCSV_UserPortalCantSeeAdminResource(t *testing.T) {
	// `azs` is ScopeAdmin in the resource registry — the user portal
	// must 404 on the export so a stale SPA never even sees the route
	// exists.
	srv := httptest.NewServer(newE2EHandler(t, ScopeUser))
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL + "/api/resources/azs/export.csv")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (azs is admin-only)", resp.StatusCode)
	}
}

func TestResourceCSV_FormatQueryFlipsJSONResponse(t *testing.T) {
	// The /api/resources/{id} huma op runs JSON unconditionally, so
	// the inline ?format=csv branch lives on the stdlib path
	// (/export.csv). Sanity-check writeRowsCSV directly through the
	// dispatcher : a synthetic request with format=csv yields CSV.
	req := httptest.NewRequest("GET", "/api/resources/azs?format=csv", nil)
	req.SetPathValue("id", "azs")
	rec := httptest.NewRecorder()
	handleResourceRows(rec, req)

	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Errorf("Content-Type = %q, want text/csv", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Code") {
		t.Errorf("CSV missing header : %q", body)
	}
}
