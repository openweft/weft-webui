// inventory_concurrency_test.go — prove the inventoryMu hot path
// holds under contention. N goroutines POST + DELETE distinct AZs
// concurrently ; the final state must reconcile (no torn rows, no
// duplicate uuids, no panic) and `go test -race` must stay clean.

package server

import (
	"fmt"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

func TestInventory_ConcurrentCreateDelete(t *testing.T) {
	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	// Reset seeded AZs so the test sees only what it inserts. The
	// dns / security stores are seeded with rows that mention DC-*
	// codes ; clearing AZs cascades nothing in this test because
	// we don't trigger DELETE on the seeded ones — only on the
	// codes we just created.
	prev := append([]map[string]any(nil), resourceByID["azs"].Rows...)
	resourceByID["azs"].Rows = []map[string]any{}
	t.Cleanup(func() { resourceByID["azs"].Rows = prev })

	const goroutines = 16
	const perGoroutine = 25
	var wg sync.WaitGroup
	var createOK, createFail, deleteOK atomic.Int32

	// Phase 1 : every goroutine creates `perGoroutine` AZs with
	// distinct codes so there are no spurious 409s. The mutex
	// guarantees serialised inserts ; the test catches races on
	// resourceByID["azs"].Rows mutation by relying on -race.
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		gi := g
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				body := map[string]any{
					"code":   fmt.Sprintf("DC-CC-%d-%d", gi, i),
					"name":   "concurrent",
					"region": "test",
					"status": "active",
					"uuid":   "",
				}
				code := hit(t, srv, "POST", "/api/azs", body, nil)
				switch code {
				case 200:
					createOK.Add(1)
				default:
					createFail.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if got := createOK.Load(); got != goroutines*perGoroutine {
		t.Errorf("createOK = %d, want %d (createFail=%d)", got, goroutines*perGoroutine, createFail.Load())
	}

	// Collect the uuids in a snapshot so the delete-phase
	// goroutines don't race a SHARED slice.
	rows := append([]map[string]any(nil), resourceByID["azs"].Rows...)
	if len(rows) != goroutines*perGoroutine {
		t.Fatalf("len(rows) = %d, want %d", len(rows), goroutines*perGoroutine)
	}
	uuids := make([]string, len(rows))
	for i, r := range rows {
		uuids[i] = str(r["uuid"])
	}

	// Phase 2 : delete all of them in parallel. Same -race watch.
	wg.Add(goroutines)
	step := len(uuids) / goroutines
	for g := 0; g < goroutines; g++ {
		start := g * step
		end := start + step
		if g == goroutines-1 {
			end = len(uuids) // mop up the remainder
		}
		go func(slice []string) {
			defer wg.Done()
			for _, u := range slice {
				code := hit(t, srv, "DELETE", "/api/azs/"+u, nil, nil)
				if code == 200 {
					deleteOK.Add(1)
				}
			}
		}(uuids[start:end])
	}
	wg.Wait()

	if got := deleteOK.Load(); got != int32(len(uuids)) {
		t.Errorf("deleteOK = %d, want %d", got, len(uuids))
	}
	if got := len(resourceByID["azs"].Rows); got != 0 {
		t.Errorf("final rows = %d, want 0", got)
	}
}

// TestInventory_UUIDsAreUnique asserts the newUUID generator
// doesn't emit collisions under contention. 16 goroutines × 1000
// iterations = 16k samples ; given an 8-byte random tail the
// birthday probability is essentially zero, so any collision means
// a code regression (e.g. someone introduced a counter that
// races).
func TestInventory_UUIDsAreUnique(t *testing.T) {
	const goroutines = 16
	const per = 1000
	var mu sync.Mutex
	seen := map[string]struct{}{}
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < per; i++ {
				u := newUUID("az")
				mu.Lock()
				if _, dup := seen[u]; dup {
					mu.Unlock()
					t.Errorf("collision : %s", u)
					return
				}
				seen[u] = struct{}{}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != goroutines*per {
		t.Errorf("got %d unique uuids, want %d", len(seen), goroutines*per)
	}
}
