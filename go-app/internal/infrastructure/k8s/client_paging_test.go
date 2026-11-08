package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// newHTTPClient returns a client with a real clientset talking to srv.
func newHTTPClient(t *testing.T, srv *httptest.Server) *DefaultK8sClient {
	t.Helper()
	clientset, err := kubernetes.NewForConfig(&rest.Config{Host: srv.URL})
	require.NoError(t, err)
	cfg := DefaultK8sClientConfig()
	cfg.RetryBackoff = time.Millisecond
	return &DefaultK8sClient{clientset: clientset, config: cfg, logger: slog.New(slog.DiscardHandler)}
}

// TestListSecrets_Pagination serves 1200 Secrets in pages of the requested
// limit: all of them come back, not the first 1000 (K8S-CLIENT-HEALTH-TIMEOUT).
func TestListSecrets_Pagination(t *testing.T) {
	const total = 1200
	var (
		mu     sync.Mutex
		limits []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/default/secrets" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		mu.Lock()
		limits = append(limits, q.Get("limit"))
		mu.Unlock()
		limit, _ := strconv.Atoi(q.Get("limit"))
		start, _ := strconv.Atoi(q.Get("continue"))
		end := min(start+limit, total)
		list := corev1.SecretList{
			TypeMeta: metav1.TypeMeta{Kind: "SecretList", APIVersion: "v1"},
		}
		for i := start; i < end; i++ {
			list.Items = append(list.Items, corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("s-%04d", i), Namespace: "default"}})
		}
		if end < total {
			list.Continue = strconv.Itoa(end)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	}))
	t.Cleanup(srv.Close)

	secrets, err := newHTTPClient(t, srv).ListSecrets(context.Background(), "default", "publishing-target=true")
	require.NoError(t, err)
	require.Len(t, secrets, total)
	assert.Equal(t, "s-0000", secrets[0].Name)
	assert.Equal(t, "s-1199", secrets[total-1].Name)
	page := strconv.Itoa(listSecretsPageSize)
	assert.Equal(t, []string{page, page, page}, limits)
}

// TestHealth_HungAPIServer: /version never answers; Health gives up at its
// own 5s deadline instead of the 30s client timeout.
func TestHealth_HungAPIServer(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})

	start := time.Now()
	err := newHTTPClient(t, srv).Health(context.Background())
	var timeout *TimeoutError
	require.ErrorAs(t, err, &timeout)
	assert.Less(t, time.Since(start), 10*time.Second)
}

func TestHealth_CancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := newHTTPClient(t, srv).Health(ctx)
	require.ErrorIs(t, err, context.Canceled)
	var timeout *TimeoutError
	assert.NotErrorAs(t, err, &timeout, "a caller's cancel is not an API timeout")
}

func TestHealth_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"major":"1","minor":"31","gitVersion":"v1.31.0"}`))
	}))
	t.Cleanup(srv.Close)
	require.NoError(t, newHTTPClient(t, srv).Health(context.Background()))
	// The fake clientset has no REST client and takes the ServerVersion path.
	require.NoError(t, createFakeClient().Health(context.Background()))
}

// TestClose_ConcurrentWithReads runs Close against in-flight reads; with
// -race it fails if Close touches state the readers use.
func TestClose_ConcurrentWithReads(t *testing.T) {
	client := createFakeClient(createTestSecret("s", "default", nil, nil))
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				_, err := client.ListSecrets(context.Background(), "default", "")
				assert.NoError(t, err)
			}
		}()
	}
	for range 5 {
		assert.NoError(t, client.Close())
	}
	wg.Wait()
}
