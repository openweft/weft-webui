package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestAPI_OpenAPIAndFlavorsEndToEnd is the cross-cutting smoke for
// the huma layer. It mounts the full API on a fresh mux, hits a
// representative sample of routes (list + get + 404 + 422), and
// dumps the generated OpenAPI to /tmp for human review.
func TestAPI_OpenAPIAndFlavorsEndToEnd(t *testing.T) {
	// Reset package singletons so the listing is deterministic.
	flavorsCatalogue = newMemFlavorCatalogue()
	scriptsCatalogue = newMemScriptCatalogue()
	sshKeysCatalogue = newMemSSHKeyCatalogue()

	mux := http.NewServeMux()
	mountAPI(mux, ScopeAdmin)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// 1. OpenAPI spec is auto-served + valid JSON with the
	// top-level shape we expect.
	resp, err := http.Get(srv.URL + "/api/openapi.json")
	if err != nil {
		t.Fatalf("openapi GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("openapi status = %d, body = %s", resp.StatusCode, body)
	}
	var spec map[string]any
	if err := json.Unmarshal(body, &spec); err != nil {
		t.Fatalf("openapi not json: %v", err)
	}
	for _, want := range []string{"openapi", "paths", "components"} {
		if _, ok := spec[want]; !ok {
			t.Errorf("openapi spec missing top-level %q : keys=%v", want, mapKeys(spec))
		}
	}

	// Persist for human review : reviewer pipes through jq.
	if err := os.WriteFile("/tmp/weft-webui-openapi.json", body, 0o644); err != nil {
		t.Logf("could not write spec sample: %v", err)
	} else {
		t.Logf("openapi spec written to /tmp/weft-webui-openapi.json (%d bytes)", len(body))
	}

	// 2. Flavors list answers with the typed envelope { flavors: [...] }.
	resp, err = http.Get(srv.URL + "/api/flavors")
	if err != nil {
		t.Fatalf("list GET: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("list status = %d", resp.StatusCode)
	}
	var listResp struct {
		Flavors []APIFlavor `json:"flavors"`
	}
	if err := json.Unmarshal(body, &listResp); err != nil {
		t.Fatalf("list body not typed: %v\n%s", err, body)
	}
	if len(listResp.Flavors) == 0 {
		t.Fatal("list returned empty — seed should have flavors")
	}
	var sawSmall bool
	for _, f := range listResp.Flavors {
		if f.Name == "small" {
			sawSmall = true
		}
	}
	if !sawSmall {
		t.Errorf("'small' not in seed listing : %+v", listResp.Flavors)
	}

	// 3. Get-one happy path.
	resp, err = http.Get(srv.URL + "/api/flavors/small")
	if err != nil {
		t.Fatalf("get GET: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("get status = %d, body = %s", resp.StatusCode, body)
	}
	var one APIFlavor
	if err := json.Unmarshal(body, &one); err != nil {
		t.Fatalf("get body: %v\n%s", err, body)
	}
	if one.Name != "small" {
		t.Errorf("get returned %q, want small", one.Name)
	}

	// 4. 404 path : structured huma error mentions the missing name.
	resp, err = http.Get(srv.URL + "/api/flavors/does-not-exist")
	if err != nil {
		t.Fatalf("get-404 GET: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("404 path status = %d, want 404", resp.StatusCode)
	}
	if !strings.Contains(string(body), "does-not-exist") {
		t.Errorf("404 body should mention the missing name : %s", body)
	}

	// 5. 422 from the maxLength tag — huma validates path params
	// before the handler runs.
	overlong := strings.Repeat("a", 200)
	resp, err = http.Get(srv.URL + "/api/flavors/" + overlong)
	if err != nil {
		t.Fatalf("get-422 GET: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 422 {
		t.Errorf("overlong name should 422 from huma validation, got %d body=%s", resp.StatusCode, body)
	}

	// 6. Scripts list endpoint is mounted under the same API.
	resp, err = http.Get(srv.URL + "/api/scripts")
	if err != nil {
		t.Fatalf("scripts list GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("/api/scripts status = %d, want 200", resp.StatusCode)
	}

	// 7. SSH-keys list endpoint is mounted under the same API.
	resp, err = http.Get(srv.URL + "/api/ssh-keys")
	if err != nil {
		t.Fatalf("ssh-keys list GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("/api/ssh-keys status = %d, want 200", resp.StatusCode)
	}
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
