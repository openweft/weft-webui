// Command dump-openapi writes the OpenAPI 3.1 spec produced by the
// huma registrations to stdout (or to a file via -o). The Svelte side
// consumes this snapshot to generate its TypeScript client via
// openapi-typescript ; running the dumper at build time keeps the
// generated types in lockstep with the Go handlers.
//
// Usage :
//
//	go run ./tools/dump-openapi > web/openapi.json
//	go run ./tools/dump-openapi -o web/openapi.json
//	go run ./tools/dump-openapi -scope admin -o web/openapi.admin.json
//
// -scope toggles between the user surface (default) and the admin
// surface. The admin spec includes the cluster-admin endpoints
// (create-tenant, network-topology, …) that aren't registered on the
// user listener.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/openweft/weft-webui/internal/server"
)

func main() {
	var (
		out   string
		scope string
	)
	flag.StringVar(&out, "o", "", "Output path (default: stdout)")
	flag.StringVar(&scope, "scope", "user", `Scope to dump: "user" or "admin"`)
	flag.Parse()

	var s server.Scope
	switch scope {
	case "user":
		s = server.ScopeUser
	case "admin":
		s = server.ScopeAdmin
	default:
		fmt.Fprintf(os.Stderr, "dump-openapi: unknown scope %q (want user/admin)\n", scope)
		os.Exit(2)
	}

	// Build a throwaway mux + huma API, marshal the spec.
	api := server.MountAPIForCodegen(http.NewServeMux(), s)
	spec := api.OpenAPI()
	b, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "dump-openapi: marshal: %v\n", err)
		os.Exit(1)
	}

	w := os.Stdout
	if out != "" {
		f, err := os.Create(out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dump-openapi: create %s: %v\n", out, err)
			os.Exit(1)
		}
		defer f.Close()
		w = f
	}
	if _, err := w.Write(b); err != nil {
		fmt.Fprintf(os.Stderr, "dump-openapi: write: %v\n", err)
		os.Exit(1)
	}
	if out != "" {
		fmt.Fprintf(os.Stderr, "dump-openapi: wrote %s (%d bytes, scope=%s)\n", out, len(b), scope)
	}
}
