package investigation

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ipiton/AMP/internal/core"
)

// dedupTTL bounds how long an incident stays claimed. The key already names
// one incident (fingerprint + StartsAt), so the TTL only caps memory and Redis
// growth; a re-fire after resolve gets a new StartsAt and a new key.
const dedupTTL = 24 * time.Hour

// inFlightMargin pads the worst-case job duration that sets a shared
// claim's in-flight TTL (see inFlightTTL in queue.go).
const inFlightMargin = 5 * time.Minute

// dedupKeyPrefix namespaces the shared claims in Redis.
const dedupKeyPrefix = "amp:investigation:dedup:"

// dedup keeps one investigation per incident (INVESTIGATION-ASYNC-DEDUP).
//
// Two levels: claimLocal runs in Submit, on the request goroutine, so it does
// no IO — an in-memory map that drops resends of an incident before they take
// a queue slot. claimShared runs in the worker and, when a Redis client is
// set, makes the claim across replicas with SETNX.
type dedup struct {
	mu        sync.Mutex
	claimed   map[string]time.Time // key -> expiry
	nextSweep time.Time
	rdb       redis.Cmdable // nil: local level only
	inFlight  time.Duration // shared claim TTL while the job runs
	now       func() time.Time
}

// dedupSweepInterval spaces out the scans for expired local claims.
const dedupSweepInterval = time.Minute

func newDedup() *dedup {
	return &dedup{claimed: make(map[string]time.Time), now: time.Now}
}

// dedupKey identifies the incident an alert belongs to, and its status: with
// only_firing off, the resolve of an incident is investigated once too.
// StartsAt is taken in whole seconds — storage keeps microseconds, so a resend
// read back from it must not differ from the first ingest in sub-second digits.
func dedupKey(alert *core.Alert) string {
	return alert.Fingerprint + ":" + strconv.FormatInt(alert.StartsAt.Unix(), 10) + ":" + string(alert.Status)
}

// claimLocal reports whether key was free on this replica and claims it.
func (d *dedup) claimLocal(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := d.now()
	if exp, ok := d.claimed[key]; ok && now.Before(exp) {
		return false
	}
	// Sweep expired claims on insert, at most once per interval: keeps the
	// map bounded by the incidents seen in one TTL without a goroutine.
	if !now.Before(d.nextSweep) {
		for k, exp := range d.claimed {
			if !now.Before(exp) {
				delete(d.claimed, k)
			}
		}
		d.nextSweep = now.Add(dedupSweepInterval)
	}
	d.claimed[key] = now.Add(dedupTTL)
	return true
}

// releaseLocal drops a local claim, so a later resend may try again.
func (d *dedup) releaseLocal(key string) {
	d.mu.Lock()
	delete(d.claimed, key)
	d.mu.Unlock()
}

// claimShared reports whether key was free across replicas and claims it for
// the in-flight TTL: a process that dies or is stopped mid-job leaves only
// this claim, so the incident reopens soon; extendShared keeps a finished
// job's claim for dedupTTL.
// Without a Redis client every claim succeeds; a Redis error is returned with
// claimed=true, so the caller investigates (fail-open) and logs it.
func (d *dedup) claimShared(ctx context.Context, key string) (bool, error) {
	if d.rdb == nil {
		return true, nil
	}
	ok, err := d.rdb.SetNX(ctx, dedupKeyPrefix+key, 1, d.inFlight).Result()
	if err != nil {
		return true, err
	}
	return ok, nil
}

// releaseShared drops a shared claim, so another attempt may run. A Redis
// error leaves the claim to expire with its TTL.
func (d *dedup) releaseShared(ctx context.Context, key string) error {
	if d.rdb == nil {
		return nil
	}
	return d.rdb.Del(ctx, dedupKeyPrefix+key).Err()
}

// extendShared keeps a finished job's claim for dedupTTL. SET rather than
// EXPIRE: a job that outlived its in-flight claim restores it.
func (d *dedup) extendShared(ctx context.Context, key string) error {
	if d.rdb == nil {
		return nil
	}
	return d.rdb.Set(ctx, dedupKeyPrefix+key, 1, dedupTTL).Err()
}
