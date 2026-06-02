// inventory_sort_test.go — pin the read-time sort the dashboard
// relies on. After inserting AZs/racks/hosts in scrambled order via
// the CRUD endpoints, GET /api/resources/<id> must return them in
// deterministic natural-key order.

package server

import (
	"net/http/httptest"
	"testing"
)

func TestSortRowsForID_AZsByCode(t *testing.T) {
	rows := []map[string]any{
		{"code": "DC-C"},
		{"code": "DC-A"},
		{"code": "DC-B"},
	}
	sorted := sortRowsForID("azs", rows)
	got := []string{
		str(sorted[0]["code"]),
		str(sorted[1]["code"]),
		str(sorted[2]["code"]),
	}
	want := []string{"DC-A", "DC-B", "DC-C"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSortRowsForID_RacksByAZThenCode(t *testing.T) {
	rows := []map[string]any{
		{"az": "DC-B", "code": "R1"},
		{"az": "DC-A", "code": "R2"},
		{"az": "DC-A", "code": "R1"},
		{"az": "DC-B", "code": "R2"},
	}
	sorted := sortRowsForID("racks", rows)
	got := make([]string, len(sorted))
	for i, r := range sorted {
		got[i] = str(r["az"]) + "/" + str(r["code"])
	}
	want := []string{"DC-A/R1", "DC-A/R2", "DC-B/R1", "DC-B/R2"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSortRowsForID_HostsByAZRackName(t *testing.T) {
	rows := []map[string]any{
		{"az": "DC-A", "rack": "R1", "name": "h2"},
		{"az": "DC-A", "rack": "R1", "name": "h1"},
		{"az": "DC-A", "rack": "R2", "name": "h1"},
		{"az": "DC-B", "rack": "R1", "name": "h1"},
	}
	sorted := sortRowsForID("hosts", rows)
	got := make([]string, len(sorted))
	for i, r := range sorted {
		got[i] = str(r["az"]) + "/" + str(r["rack"]) + "/" + str(r["name"])
	}
	want := []string{"DC-A/R1/h1", "DC-A/R1/h2", "DC-A/R2/h1", "DC-B/R1/h1"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSortRowsForID_DoesNotMutateInput(t *testing.T) {
	// Insertion order must survive — the persistence layer + other
	// callers (events.go, the registry walker) iterate res.Rows and
	// expect it untouched.
	input := []map[string]any{
		{"code": "DC-Z"},
		{"code": "DC-A"},
	}
	_ = sortRowsForID("azs", input)
	if str(input[0]["code"]) != "DC-Z" || str(input[1]["code"]) != "DC-A" {
		t.Errorf("input was mutated : %+v", input)
	}
}

func TestSortRowsForID_UnknownIDIsNoop(t *testing.T) {
	input := []map[string]any{{"k": "a"}, {"k": "b"}}
	got := sortRowsForID("not-inventory", input)
	if len(got) != 2 || str(got[0]["k"]) != "a" || str(got[1]["k"]) != "b" {
		t.Errorf("non-inventory id should pass through unchanged ; got %+v", got)
	}
}

// End-to-end : insert AZs in reverse order via the CRUD API, then
// GET /api/resources/azs and confirm the response is alphabetised.
func TestInventoryAPI_AZListIsSortedAfterScrambledInsert(t *testing.T) {
	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	// Reset the seeded AZs so the test sees only what we insert.
	prev := append([]map[string]any(nil), resourceByID["azs"].Rows...)
	resourceByID["azs"].Rows = []map[string]any{}
	t.Cleanup(func() { resourceByID["azs"].Rows = prev })

	for _, code := range []string{"DC-Z", "DC-A", "DC-M"} {
		body := map[string]any{
			"code": code, "name": code + " Datacenter", "region": "test",
			"status": "active", "uuid": "",
		}
		if got := hit(t, srv, "POST", "/api/azs", body, nil); got != 200 {
			t.Fatalf("POST %s: status %d", code, got)
		}
	}

	var listResp struct {
		Rows []map[string]any `json:"rows"`
	}
	if got := hit(t, srv, "GET", "/api/resources/azs", nil, &listResp); got != 200 {
		t.Fatalf("GET azs: status %d", got)
	}
	codes := make([]string, len(listResp.Rows))
	for i, r := range listResp.Rows {
		codes[i] = str(r["code"])
	}
	want := []string{"DC-A", "DC-M", "DC-Z"}
	if len(codes) != len(want) {
		t.Fatalf("len(rows) = %d, want %d ; codes=%+v", len(codes), len(want), codes)
	}
	for i := range want {
		if codes[i] != want[i] {
			t.Errorf("codes[%d] = %q, want %q (full list : %+v)", i, codes[i], want[i], codes)
		}
	}
}
