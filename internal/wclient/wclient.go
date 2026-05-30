// Package wclient is the thin adapter between the weft-webui HTTP handlers
// and the real weft control-plane gRPC API (vzd / weft-client). It hides
// dialing + connection caching, and translates proto messages into the same
// map[string]any row shape the dashboard's frontend already consumes.
//
// When the server is started without --weft-socket, the live client is nil
// and every handler falls back to its mock implementation. When a socket
// is provided, the resources we have wired call into the daemon ; the
// others stay mock until they're wired one at a time.
package wclient

import (
	"context"
	"errors"
	"sync"
	"time"

	vzclient "github.com/openweft/weft-client"
	vzdv1 "github.com/openweft/weft-proto"
	"google.golang.org/grpc"
)

// Client is a lazily-dialed gRPC client to a local (or SSH-tunneled) weft
// daemon. Safe for concurrent use — every method acquires the connection on
// first call and reuses it for the lifetime of the process.
type Client struct {
	socket string
	mu     sync.Mutex
	conn   *grpc.ClientConn
	rpc    vzdv1.VzdServiceClient
}

// New builds a client that will dial socket on the first RPC. socket follows
// the weft-client convention : a unix path (e.g. ~/.vzd/vzd.sock) or an
// ssh:// URL routed through the SSH transport.
func New(socket string) *Client { return &Client{socket: socket} }

func (c *Client) dial() (vzdv1.VzdServiceClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rpc != nil {
		return c.rpc, nil
	}
	rpc, conn, err := vzclient.Client(c.socket)
	if err != nil {
		return nil, err
	}
	c.rpc, c.conn = rpc, conn
	return c.rpc, nil
}

// Close releases the cached connection (best-effort).
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn, c.rpc = nil, nil
	return err
}

// ctx returns a context derived from the caller with a short RPC deadline.
func rpcCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 5*time.Second)
}

// ListProjects returns the daemon's projects as dashboard rows. The keys
// match the columns the frontend expects for the "projects" resource :
// name / uuid / created.
func (c *Client) ListProjects(ctx context.Context) ([]map[string]any, error) {
	rpc, err := c.dial()
	if err != nil {
		return nil, err
	}
	cctx, cancel := rpcCtx(ctx)
	defer cancel()
	resp, err := rpc.ListProjects(cctx, &vzdv1.ListProjectsRequest{})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("nil ListProjects response")
	}
	out := make([]map[string]any, 0, len(resp.Projects))
	for _, p := range resp.Projects {
		out = append(out, map[string]any{
			"name":    p.Name,
			"uuid":    p.Uuid,
			"created": time.Unix(0, p.CreatedAtUnixNs).UTC().Format("2006-01-02"),
		})
	}
	return out, nil
}
