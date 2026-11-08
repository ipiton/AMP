package upstreamai

import (
	"errors"
	"testing"

	"github.com/prometheus/alertmanager/alert"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
)

type tracer struct {
	name  string
	calls *[]string
	err   error
}

func (t tracer) PreStore(*alert.Alert, bool) error {
	*t.calls = append(*t.calls, t.name+".pre")
	return t.err
}
func (t tracer) PostStore(*alert.Alert, bool) { *t.calls = append(*t.calls, t.name+".post") }
func (t tracer) PostDelete(*alert.Alert)      { *t.calls = append(*t.calls, t.name+".delete") }
func (t tracer) PostGC(model.Fingerprints)    { *t.calls = append(*t.calls, t.name+".gc") }

func TestChainCallsInOrder(t *testing.T) {
	var calls []string
	c := Chain(tracer{name: "a", calls: &calls}, tracer{name: "b", calls: &calls})
	a := firing("DiskFull")

	assert.NoError(t, c.PreStore(a, false))
	c.PostStore(a, false)
	c.PostDelete(a)
	c.PostGC(nil)

	assert.Equal(t, []string{"a.pre", "b.pre", "a.post", "b.post", "a.delete", "b.delete", "a.gc", "b.gc"}, calls)
}

func TestChainPreStoreStopsAtFirstError(t *testing.T) {
	var calls []string
	reject := errors.New("rejected")
	c := Chain(tracer{name: "a", calls: &calls, err: reject}, tracer{name: "b", calls: &calls})

	assert.ErrorIs(t, c.PreStore(firing("DiskFull"), false), reject)
	assert.Equal(t, []string{"a.pre"}, calls)
}
