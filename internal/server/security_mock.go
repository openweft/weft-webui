// security_mock.go — mock-friendly rules store for security groups.
//
// The live setSecurityGroupRules takes a uuid + full rule list and
// replaces atomically. In mock mode (no live agent) the dashboard
// still needs working edit/delete, so this file keeps an in-memory
// map keyed by group uuid. The initial seed is derived from the
// static "security-rules" rows in resources.go, matched by group
// name (since seed rules carry `group` = name, not uuid).
//
// Reads fall back to this store on Unimplemented ; writes always
// go here when live is nil.

package server

import (
	"sync"

	"github.com/openweft/weft-webui/internal/wclient"
)

var (
	sgRulesMu sync.Mutex
	sgRules   = map[string][]wclient.SecurityRule{}
)

// sgRulesSeed derives the initial mock rule list for a group from
// its name. Called lazily on first read so the seed table is fully
// initialised by then.
func sgRulesSeed(groupName string) []wclient.SecurityRule {
	rr, ok := resourceByID["security-rules"]
	if !ok {
		return nil
	}
	var out []wclient.SecurityRule
	for _, row := range rr.Rows {
		if str(row["group"]) != groupName {
			continue
		}
		out = append(out, wclient.SecurityRule{
			Direction:  parseDirection(str(row["direction"])),
			Protocol:   parseProtocol(str(row["protocol"])),
			PortMin:    portFromRange(str(row["port_range"]), true),
			PortMax:    portFromRange(str(row["port_range"]), false),
			RemoteCIDR: str(row["remote"]),
			Enabled:    true,
		})
	}
	return out
}

func parseDirection(s string) string {
	if s == "egress" {
		return "egress"
	}
	return "ingress"
}

func parseProtocol(s string) string {
	switch s {
	case "tcp", "udp", "icmp", "any":
		return s
	default:
		return "tcp"
	}
}

// portFromRange parses "443" or "80-90" ; returns min or max
// depending on the flag. Returns 0 for "any".
func portFromRange(s string, min bool) int32 {
	if s == "" || s == "any" {
		return 0
	}
	var lo, hi int32
	for i := 0; i < len(s); i++ {
		if s[i] == '-' {
			lo = atoi32(s[:i])
			hi = atoi32(s[i+1:])
			if min {
				return lo
			}
			return hi
		}
	}
	p := atoi32(s)
	return p
}

func atoi32(s string) int32 {
	var n int32
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int32(c-'0')
	}
	return n
}

// findSGNameByUUID looks up a security-group row by its uuid and
// returns the group's name. Used to bridge between live uuid-keyed
// APIs and the mock name-keyed rules seed.
func findSGNameByUUID(uuid string) (string, bool) {
	res, ok := resourceByID["security-groups"]
	if !ok {
		return "", false
	}
	for _, row := range res.Rows {
		if str(row["uuid"]) == uuid {
			return str(row["name"]), true
		}
	}
	return "", false
}

func getMockSGRules(uuid string) []wclient.SecurityRule {
	sgRulesMu.Lock()
	defer sgRulesMu.Unlock()
	if rs, ok := sgRules[uuid]; ok {
		return rs
	}
	// Seed from the static rules table on first read.
	name, ok := findSGNameByUUID(uuid)
	if !ok {
		return nil
	}
	seed := sgRulesSeed(name)
	sgRules[uuid] = seed
	return seed
}

func setMockSGRules(uuid string, rules []wclient.SecurityRule) {
	sgRulesMu.Lock()
	defer sgRulesMu.Unlock()
	if rules == nil {
		rules = []wclient.SecurityRule{}
	}
	sgRules[uuid] = rules
	// Mirror the rule count back to the SG row so the dashboard
	// table stays in sync without a reload-from-scratch.
	if res, ok := resourceByID["security-groups"]; ok {
		for _, row := range res.Rows {
			if str(row["uuid"]) == uuid {
				row["rules"] = len(rules)
				break
			}
		}
	}
}
