module github.com/openweft/weft-webui

go 1.26

require (
	github.com/coreos/go-oidc/v3 v3.18.0
	github.com/danielgtaylor/huma/v2 v2.38.0
	github.com/openweft/weft-client v0.2.2
	github.com/openweft/weft-network-proto v0.1.0
	github.com/openweft/weft-proto v0.8.0
	github.com/prometheus/client_golang v1.20.5
	golang.org/x/oauth2 v0.36.0
	golang.org/x/time v0.15.0
	google.golang.org/grpc v1.80.0
)

require (
	github.com/agext/levenshtein v1.2.1 // indirect
	github.com/apparentlymart/go-textseg/v15 v15.0.0 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-jose/go-jose/v4 v4.1.4 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/grpc-transports/ssh v0.2.0 // indirect
	github.com/hashicorp/hcl/v2 v2.24.0 // indirect
	github.com/klauspost/compress v1.18.5 // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.1 // indirect
	github.com/prometheus/common v0.55.0 // indirect
	github.com/prometheus/procfs v0.15.1 // indirect
	github.com/zclconf/go-cty v1.18.1 // indirect
	golang.org/x/crypto v0.50.0 // indirect
	golang.org/x/mod v0.34.0 // indirect
	golang.org/x/net v0.53.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	golang.org/x/tools v0.43.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260120221211-b8f7ae30c516 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

// Dev-loop : weft-proto v0.6.0 tag is committed locally but not yet
// pushed at the time of writing (no SSH key in agent during the
// release prep session). Workspace mode (../go.work) already routes
// the import to the in-tree sibling, so this replace is belt-and-
// suspenders for module-mode builds. Drop once the tag is published
// and `go mod tidy` succeeds.
replace github.com/openweft/weft-proto => ../weft-proto
