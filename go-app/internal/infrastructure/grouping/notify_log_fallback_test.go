package grouping

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const fallbackGroupKey = GroupKey("receiver=default/alertname=X")

func TestResilientNotifyLog_ReadablePrimaryAnswers(t *testing.T) {
	ctx := context.Background()
	primary := &unreadableNotifyLog{notifyDedupLog: newNotifyDedupLog()}
	log := NewResilientNotifyLog(primary)
	now := time.Now()

	// On record in the shared log only, as after a send by another replica.
	require.NoError(t, primary.notifyDedupLog.RecordSent(ctx, fallbackGroupKey, "t", "a:firing", now, time.Hour))

	dup, err := log.IsDuplicate(ctx, fallbackGroupKey, "t", "a:firing", now.Add(-time.Hour))
	require.NoError(t, err)
	require.True(t, dup)

	dup, err = log.IsDuplicate(ctx, fallbackGroupKey, "t", "b:firing", now.Add(-time.Hour))
	require.NoError(t, err)
	require.False(t, dup)
}

func TestResilientNotifyLog_UnreadablePrimary(t *testing.T) {
	ctx := context.Background()
	primary := &unreadableNotifyLog{notifyDedupLog: newNotifyDedupLog()}
	log := NewResilientNotifyLog(primary)
	now := time.Now()
	since := now.Add(-time.Hour)

	require.NoError(t, log.RecordSent(ctx, fallbackGroupKey, "mine", "a:firing", now, time.Hour))
	require.NoError(t, primary.notifyDedupLog.RecordSent(ctx, fallbackGroupKey, "theirs", "a:firing", now, time.Hour))
	primary.fail.Store(true)

	t.Run("own send is a duplicate, marked as a local answer", func(t *testing.T) {
		dup, err := log.IsDuplicate(ctx, fallbackGroupKey, "mine", "a:firing", since)
		require.True(t, dup)
		require.ErrorIs(t, err, ErrNotifyLogAnsweredLocally)
		require.ErrorIs(t, err, errNotifyLogDown, "the cause must stay visible")
	})

	t.Run("a set this replica did not send is not a duplicate", func(t *testing.T) {
		dup, err := log.IsDuplicate(ctx, fallbackGroupKey, "mine", "a:firing|b:firing", since)
		require.False(t, dup)
		require.ErrorIs(t, err, errNotifyLogDown)
		require.NotErrorIs(t, err, ErrNotifyLogAnsweredLocally)
	})

	t.Run("another replica's send is unknown here", func(t *testing.T) {
		dup, err := log.IsDuplicate(ctx, fallbackGroupKey, "theirs", "a:firing", since)
		require.False(t, dup)
		require.ErrorIs(t, err, errNotifyLogDown)
		require.NotErrorIs(t, err, ErrNotifyLogAnsweredLocally)
	})

	t.Run("own send older than repeat_interval is due again", func(t *testing.T) {
		dup, err := log.IsDuplicate(ctx, fallbackGroupKey, "mine", "a:firing", now.Add(time.Minute))
		require.False(t, dup)
		require.NotErrorIs(t, err, ErrNotifyLogAnsweredLocally)
	})
}

func TestResilientNotifyLog_ForgetClearsOwnRecord(t *testing.T) {
	ctx := context.Background()
	primary := &unreadableNotifyLog{notifyDedupLog: newNotifyDedupLog()}
	log := NewResilientNotifyLog(primary)
	now := time.Now()

	require.NoError(t, log.RecordSent(ctx, fallbackGroupKey, "t", "a:firing", now, time.Hour))
	require.NoError(t, log.Forget(ctx, fallbackGroupKey))
	primary.fail.Store(true)

	dup, err := log.IsDuplicate(ctx, fallbackGroupKey, "t", "a:firing", now.Add(-time.Hour))
	require.False(t, dup, "a deleted group must notify again when it comes back")
	require.NotErrorIs(t, err, ErrNotifyLogAnsweredLocally)
}

// The own record has no TTL store behind it, so it is swept on RecordSent.
func TestResilientNotifyLog_EvictsExpiredOwnRecords(t *testing.T) {
	ctx := context.Background()
	log := NewResilientNotifyLog(newNotifyDedupLog()).(*resilientNotifyLog)
	start := time.Now()

	for _, group := range []GroupKey{"g1", "g2", "g3"} {
		require.NoError(t, log.RecordSent(ctx, group, "t", "a:firing", start, time.Minute))
	}
	require.Len(t, log.local.entries, 3)

	// A send long after the others expired triggers the sweep.
	require.NoError(t, log.RecordSent(ctx, "g4", "t", "a:firing", start.Add(24*time.Hour), time.Minute))

	require.Len(t, log.local.entries, 1)
	_, kept := log.local.entries[dedupKey{groupKey: "g4", target: "t"}]
	require.True(t, kept)
}

// claimCountingLog records calls to a method the wrapper does not override.
type claimCountingLog struct {
	*notifyDedupLog
	claims int
}

func (l *claimCountingLog) TryClaim(context.Context, GroupKey, time.Duration) (bool, func() error, error) {
	l.claims++
	return false, nil, errors.New("claim refused")
}

func TestResilientNotifyLog_OtherMethodsGoToPrimary(t *testing.T) {
	primary := &claimCountingLog{notifyDedupLog: newNotifyDedupLog()}
	log := NewResilientNotifyLog(primary)

	claimed, _, err := log.TryClaim(context.Background(), fallbackGroupKey, time.Minute)

	require.False(t, claimed)
	require.EqualError(t, err, "claim refused")
	require.Equal(t, 1, primary.claims)
}
