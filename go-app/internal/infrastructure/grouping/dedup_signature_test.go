package grouping

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignatureCovers(t *testing.T) {
	tests := []struct {
		name    string
		sent    string
		current string
		want    bool
	}{
		{"same set", "a:firing|b:firing", "a:firing|b:firing", true},
		{"group shrank", "a:firing|b:firing", "b:firing", true},
		{"resolved alert notified and pruned", "a:resolved|b:firing", "b:firing", true},
		{"new alert", "a:firing", "a:firing|b:firing", false},
		{"alert resolved", "a:firing|b:firing", "a:resolved|b:firing", false},
		{"alert fired again after its resolve was sent", "a:resolved|b:firing", "a:firing|b:firing", false},
		{"different alert, same size", "a:firing", "b:firing", false},
		{"nothing sent yet", "", "a:firing", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, signatureCovers(tt.sent, tt.current))
		})
	}
}

// Both notification log implementations apply the same rule: a set already
// covered by the last notification is a duplicate, a set with something new
// is not.
func TestIsDuplicate_SubsetOfLastNotification(t *testing.T) {
	redisLog, _, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()

	logs := map[string]GroupNotifyLog{
		"memory": newNotifyDedupLog(),
		"redis":  redisLog,
	}
	for name, log := range logs {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			groupKey := GroupKey("receiver=default/alertname=X")
			now := time.Now()
			since := now.Add(-time.Hour)
			require.NoError(t, log.RecordSent(ctx, groupKey, "t", "a:resolved|b:firing", now, time.Hour))

			cases := []struct {
				signature string
				want      bool
			}{
				{"a:resolved|b:firing", true},
				{"b:firing", true},
				{"b:firing|c:firing", false},
				{"a:firing|b:firing", false},
				{"b:resolved", false},
			}
			for _, c := range cases {
				dup, err := log.IsDuplicate(ctx, groupKey, "t", c.signature, since)
				require.NoError(t, err)
				assert.Equal(t, c.want, dup, "signature %q", c.signature)
			}

			// Covered, but repeat_interval has passed since the notification.
			dup, err := log.IsDuplicate(ctx, groupKey, "t", "b:firing", now.Add(time.Minute))
			require.NoError(t, err)
			assert.False(t, dup, "a reminder is due after repeat_interval")

			// Another target of the same group has its own record.
			dup, err = log.IsDuplicate(ctx, groupKey, "other", "b:firing", since)
			require.NoError(t, err)
			assert.False(t, dup)
		})
	}
}
