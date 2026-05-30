// network_handlers.go — Create / Delete handlers for the resources
// owned by the weft-network controller (Routers, Load Balancers,
// DNS Zones, DNS Records). All handlers moved to api_networking.go
// (huma) ; the requireLiveNet helper is gone with them — the huma
// version is requireLiveNetCtx, defined alongside the operations.
package server
