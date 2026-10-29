package tools_test

import (
	"context"
	"testing"

	"github.com/ipiton/AMP/internal/core/investigation"
	"github.com/ipiton/AMP/internal/infrastructure/investigation/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// anyNamespace opts the action tests out of the namespace allowlist, which
// has its own tests below.
var anyNamespace = []string{"*"}

// listedNamespaces records the namespace of every API call the fake client
// receives.
func listedNamespaces(cs *fake.Clientset) *[]string {
	var seen []string
	cs.PrependReactor("*", "*", func(a k8stesting.Action) (bool, runtime.Object, error) {
		seen = append(seen, a.GetNamespace())
		return false, nil, nil
	})
	return &seen
}

func TestKubernetesTool_NamespaceAllowlist(t *testing.T) {
	cases := []struct {
		name      string
		allowed   []string
		alertNS   string // "" = no namespace label
		requested string // "" = model passes no namespace
		wantNS    string // namespace the API call goes to; "" = refused
		wantErr   string
	}{
		{name: "empty allowlist, default to alert namespace", alertNS: "payments", wantNS: "payments"},
		{name: "empty allowlist, alert namespace requested", alertNS: "payments", requested: "payments", wantNS: "payments"},
		{name: "empty allowlist, other namespace refused", alertNS: "payments", requested: "kube-system", wantErr: `namespace "kube-system" not allowed`},
		{name: "empty allowlist, alert without namespace", requested: "", wantErr: "no namespace"},
		{name: "empty allowlist, alert without namespace, explicit request refused", requested: "default", wantErr: `namespace "default" not allowed`},
		{name: "explicit list allows listed", allowed: []string{"payments", "orders"}, alertNS: "payments", requested: "orders", wantNS: "orders"},
		{name: "explicit list refuses alert namespace outside it", allowed: []string{"orders"}, alertNS: "payments", wantErr: `namespace "payments" not allowed`},
		{name: "explicit list refuses other", allowed: []string{"orders"}, alertNS: "orders", requested: "kube-system", wantErr: "allowed namespaces are [orders]"},
		{name: "wildcard allows any", allowed: []string{"*"}, alertNS: "payments", requested: "kube-system", wantNS: "kube-system"},
		{name: "wildcard, no namespace anywhere, never all-namespaces", allowed: []string{"*"}, wantErr: "no namespace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs := fake.NewSimpleClientset()
			seen := listedNamespaces(cs)
			tool := tools.NewKubernetesTool(cs, tc.allowed)

			ctx := context.Background()
			if tc.alertNS != "" {
				ctx = investigation.WithAlertNamespace(ctx, tc.alertNS)
			}
			params := map[string]any{"action": "list_pods"}
			if tc.requested != "" {
				params["namespace"] = tc.requested
			}

			result, err := tool.Execute(ctx, params)
			require.NoError(t, err)
			if tc.wantErr != "" {
				assert.True(t, result.IsError)
				assert.Contains(t, result.Error, tc.wantErr)
				assert.Empty(t, *seen, "a refused call must not reach the API")
				return
			}
			assert.False(t, result.IsError, result.Error)
			assert.Equal(t, []string{tc.wantNS}, *seen)
		})
	}
}

// Every action goes through the same check: get_logs is the one that leaks
// the most.
func TestKubernetesTool_NamespaceAllowlist_AllActions(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "kube-system"}}
	for _, action := range []string{"list_pods", "get_pod", "get_events", "get_logs", "get_deployments"} {
		cs := fake.NewSimpleClientset(pod)
		seen := listedNamespaces(cs)
		tool := tools.NewKubernetesTool(cs, nil)
		ctx := investigation.WithAlertNamespace(context.Background(), "payments")

		result, err := tool.Execute(ctx, map[string]any{"action": action, "namespace": "kube-system", "name": "p"})
		require.NoError(t, err)
		assert.True(t, result.IsError, action)
		assert.Empty(t, *seen, action)
	}
}

func TestKubernetesTool_DefinitionHasNoDefaultNamespace(t *testing.T) {
	def := tools.NewKubernetesTool(fake.NewSimpleClientset(), nil).Definition()
	assert.Nil(t, def.Parameters.Properties["namespace"].Default,
		"a schema default would make the model pass a namespace instead of falling back to the alert's")
}
