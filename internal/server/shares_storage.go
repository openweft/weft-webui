// shares_storage.go — in-memory store for the POSIX (RWX) shares
// browser. The HTTP handlers moved to api_storage.go (huma) ; this
// file owns the per-share file maps so the browser endpoints can
// list / get / put / delete by share name.
//
// Production note : the seed has been removed. Operators see empty
// shares until real content lands via the underlying CubeFS mount.
// The full live wire-up replaces this map with a virtio-fs / SFTP
// projection driven by the share mount path on the agent.
package server

import "sync"

var (
	sharesMu   sync.Mutex
	shareFiles = map[string][]s3object{}
)
