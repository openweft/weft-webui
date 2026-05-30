// networktopology.go — sval helper used by the network-topology huma
// operation in api_networking.go. The handler itself lives there ;
// this file keeps the helper because it's also used outside the
// topology call.
package server

// sval safely extracts a string field from a map row produced by the
// resource registry. Bogus rows yield "" rather than panicking.
func sval(m map[string]any, k string) string {
	if v, ok := m[k]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
