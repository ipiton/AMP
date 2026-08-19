package grouping

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// === Task rec fix round 1 (review finding I1): per-GroupKey publish lock ===

// TestGroupPublishLocks_DifferentKeysDoNotSerialize is the finding itself:
// with the old 256-way striped implementation two group keys could share a
// stripe and then serialize for a whole delivery-confirmation wait. A real
// per-key lock must let them proceed concurrently regardless of hashing.
func TestGroupPublishLocks_DifferentKeysDoNotSerialize(t *testing.T) {
	locks := newGroupPublishLocks()

	releaseA := locks.acquire(GroupKey("receiver=default/alertname=A"))
	defer releaseA()

	acquired := make(chan struct{})
	go func() {
		release := locks.acquire(GroupKey("receiver=default/alertname=B"))
		close(acquired)
		release()
	}()

	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("a different group key must not block on another group's held lock")
	}
}

// TestGroupPublishLocks_SameKeySerializes: the invariant the lock exists for
// (task 2.4 Finding 4) — the check-publish-record sequence for ONE group must
// never interleave with itself.
func TestGroupPublishLocks_SameKeySerializes(t *testing.T) {
	locks := newGroupPublishLocks()
	key := GroupKey("receiver=default/alertname=A")

	release := locks.acquire(key)

	acquired := make(chan struct{})
	go func() {
		second := locks.acquire(key)
		close(acquired)
		second()
	}()

	select {
	case <-acquired:
		t.Fatal("the same group key must serialize")
	case <-time.After(50 * time.Millisecond):
	}

	release()

	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("the waiter must proceed once the holder releases")
	}
}

// TestGroupPublishLocks_EntriesAreReclaimed: the map must not grow with every
// group ever fired — that unbounded growth is why the original implementation
// chose striping. Refcounting is what makes a real per-key lock affordable.
func TestGroupPublishLocks_EntriesAreReclaimed(t *testing.T) {
	locks := newGroupPublishLocks()

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := GroupKey("receiver=default/alertname=" + string(rune('A'+i%26)) + string(rune('0'+i/26)))
			release := locks.acquire(key)
			release()
		}(i)
	}
	wg.Wait()

	assert.Zero(t, locks.tracked(), "every lock entry must be reclaimed once its last holder releases")
}

// TestGroupPublishLocks_ConcurrentSameKeyIsMutuallyExclusive proves mutual
// exclusion under load, and (with -race) that refcount bookkeeping does not
// hand two goroutines the same critical section.
func TestGroupPublishLocks_ConcurrentSameKeyIsMutuallyExclusive(t *testing.T) {
	locks := newGroupPublishLocks()
	key := GroupKey("receiver=default/alertname=A")

	inside := 0
	maxInside := 0
	var stateMu sync.Mutex

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release := locks.acquire(key)
			stateMu.Lock()
			inside++
			if inside > maxInside {
				maxInside = inside
			}
			stateMu.Unlock()

			stateMu.Lock()
			inside--
			stateMu.Unlock()
			release()
		}()
	}
	wg.Wait()

	require.Equal(t, 1, maxInside, "at most one goroutine may hold one group's publish lock")
	assert.Zero(t, locks.tracked())
}
