package diagnoses

import (
	"testing"
	"time"
)

func TestCache_AddDedupByPatternHash(t *testing.T) {
	c, _ := NewCache(Options{})
	defer c.Close()

	c.Add(Diagnosis{PatternHash: "abc", Severity: SeverityCritical, Title: "first", Occurrences: 5})
	c.Add(Diagnosis{PatternHash: "abc", Severity: SeverityCritical, Title: "second", Occurrences: 12})
	snap := c.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("want 1 entry after dedup ; got %d", len(snap))
	}
	if snap[0].Title != "second" {
		t.Errorf("dedup kept first instead of latest : %v", snap[0])
	}
}

func TestCache_SnapshotSortedBySeverityThenOccurrences(t *testing.T) {
	c, _ := NewCache(Options{})
	defer c.Close()
	c.Add(Diagnosis{PatternHash: "a", Severity: SeverityLow, Title: "low-a", Occurrences: 1})
	c.Add(Diagnosis{PatternHash: "b", Severity: SeverityCritical, Title: "crit-b", Occurrences: 1})
	c.Add(Diagnosis{PatternHash: "c", Severity: SeverityHigh, Title: "high-c", Occurrences: 100})
	c.Add(Diagnosis{PatternHash: "d", Severity: SeverityCritical, Title: "crit-d", Occurrences: 50})

	snap := c.Snapshot()
	want := []string{"crit-d", "crit-b", "high-c", "low-a"}
	for i, w := range want {
		if snap[i].Title != w {
			t.Errorf("snap[%d].Title = %q ; want %q", i, snap[i].Title, w)
		}
	}
}

func TestCache_RejectInvalidDiagnoses(t *testing.T) {
	c, _ := NewCache(Options{})
	defer c.Close()
	// Empty pattern_hash — rejected.
	c.Add(Diagnosis{Severity: SeverityCritical, Title: "no hash"})
	if len(c.Snapshot()) != 0 {
		t.Error("empty-hash diagnosis should be dropped")
	}
}

func TestCache_EvictionAtMaxRecent(t *testing.T) {
	c, _ := NewCache(Options{MaxRecent: 3})
	defer c.Close()
	// 4 patterns with distinct LastSeen, oldest first.
	for i := 0; i < 4; i++ {
		c.Add(Diagnosis{
			PatternHash: string(rune('a' + i)),
			Severity:    SeverityLow,
			Title:       "diag",
			LastSeen:    time.Unix(int64(i), 0),
		})
	}
	snap := c.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("want 3 entries after cap ; got %d", len(snap))
	}
	for _, d := range snap {
		if d.PatternHash == "a" {
			t.Error("oldest entry (a) should have been evicted")
		}
	}
}

func TestCache_SubscribeReceivesAdds(t *testing.T) {
	c, _ := NewCache(Options{})
	defer c.Close()
	ch := c.Subscribe()

	c.Add(Diagnosis{PatternHash: "abc", Severity: SeverityHigh, Title: "alert"})
	select {
	case got := <-ch:
		if got.PatternHash != "abc" {
			t.Errorf("received %q ; want abc", got.PatternHash)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Subscribe channel did not receive added diagnosis")
	}
}

func TestCache_SubscribeDropsForSlowConsumers(t *testing.T) {
	c, _ := NewCache(Options{})
	defer c.Close()
	_ = c.Subscribe() // never read — buffer 16, will overflow

	// 100 adds should NOT block (drops are silent).
	for i := 0; i < 100; i++ {
		c.Add(Diagnosis{PatternHash: "x", Severity: SeverityLow, Title: "spam"})
	}
}

func TestNewCache_OfflineMode(t *testing.T) {
	// Empty NATSURL is the supported offline mode (dev / tests /
	// degraded cluster) ; no error, empty cache, manual Add still
	// works.
	c, err := NewCache(Options{})
	if err != nil {
		t.Fatalf("offline NewCache returned error : %v", err)
	}
	defer c.Close()
	if len(c.Snapshot()) != 0 {
		t.Errorf("offline cache should start empty")
	}
}

func TestValidate(t *testing.T) {
	for _, tc := range []struct {
		name string
		d    Diagnosis
		ok   bool
	}{
		{"full", Diagnosis{PatternHash: "h", Severity: SeverityCritical, Title: "t"}, true},
		{"missing hash", Diagnosis{Severity: SeverityCritical, Title: "t"}, false},
		{"missing title", Diagnosis{PatternHash: "h", Severity: SeverityCritical}, false},
		{"bad severity", Diagnosis{PatternHash: "h", Severity: "weird", Title: "t"}, false},
		{"all severities", Diagnosis{PatternHash: "h", Severity: SeverityLow, Title: "t"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validate(tc.d)
			gotOK := err == nil
			if gotOK != tc.ok {
				t.Errorf("validate ok=%v ; want %v (err=%v)", gotOK, tc.ok, err)
			}
		})
	}
}
