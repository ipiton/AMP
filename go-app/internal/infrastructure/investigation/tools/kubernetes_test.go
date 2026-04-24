package tools_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ipiton/AMP/internal/infrastructure/investigation/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestKubernetesTool_Definition(t *testing.T) {
	tool := tools.NewKubernetesTool(fake.NewSimpleClientset())
	def := tool.Definition()
	assert.Equal(t, "kubernetes_action", def.Name)
	assert.Contains(t, def.Parameters.Required, "action")
	assert.Contains(t, def.Parameters.Properties, "namespace")
	assert.Contains(t, def.Parameters.Properties, "name")
	assert.Contains(t, def.Parameters.Properties, "container")
}

func TestKubernetesTool_Execute_UnknownAction(t *testing.T) {
	tool := tools.NewKubernetesTool(fake.NewSimpleClientset())
	result, err := tool.Execute(context.Background(), map[string]any{"action": "bad_action"})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Error, "unknown action")
}

func TestKubernetesTool_Execute_MissingAction(t *testing.T) {
	tool := tools.NewKubernetesTool(fake.NewSimpleClientset())
	result, err := tool.Execute(context.Background(), map[string]any{})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Error, "missing required param")
}

func TestKubernetesTool_ListPods(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-pod-abc",
			Namespace: "default",
			Labels:    map[string]string{"app": "api"},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{Ready: true, RestartCount: 2},
			},
		},
	}
	tool := tools.NewKubernetesTool(fake.NewSimpleClientset(pod))

	result, err := tool.Execute(context.Background(), map[string]any{
		"action":    "list_pods",
		"namespace": "default",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError, result.Error)

	var pods []map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.Content), &pods))
	require.Len(t, pods, 1)
	assert.Equal(t, "api-pod-abc", pods[0]["name"])
	assert.Equal(t, "Running", pods[0]["phase"])
	assert.Equal(t, true, pods[0]["ready"])
	assert.EqualValues(t, 2, pods[0]["restarts"])
}

func TestKubernetesTool_GetPod(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "my-pod", Namespace: "staging"},
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	}
	tool := tools.NewKubernetesTool(fake.NewSimpleClientset(pod))

	result, err := tool.Execute(context.Background(), map[string]any{
		"action":    "get_pod",
		"namespace": "staging",
		"name":      "my-pod",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError, result.Error)

	var p map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.Content), &p))
	assert.Equal(t, "my-pod", p["name"])
	assert.Equal(t, "Pending", p["phase"])
}

func TestKubernetesTool_GetPod_MissingName(t *testing.T) {
	tool := tools.NewKubernetesTool(fake.NewSimpleClientset())
	result, err := tool.Execute(context.Background(), map[string]any{
		"action": "get_pod",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Error, "requires 'name'")
}

func TestKubernetesTool_GetEvents(t *testing.T) {
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "evt1", Namespace: "default"},
		InvolvedObject: corev1.ObjectReference{
			Kind: "Pod",
			Name: "api-pod",
		},
		Type:    "Warning",
		Reason:  "OOMKilled",
		Message: "Container killed due to OOM",
		Count:   3,
	}
	tool := tools.NewKubernetesTool(fake.NewSimpleClientset(event))

	result, err := tool.Execute(context.Background(), map[string]any{
		"action":    "get_events",
		"namespace": "default",
		"name":      "api-pod",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError, result.Error)

	var events []map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.Content), &events))
	require.Len(t, events, 1)
	assert.Equal(t, "Warning", events[0]["type"])
	assert.Equal(t, "OOMKilled", events[0]["reason"])
}

func TestKubernetesTool_GetDeployments(t *testing.T) {
	replicas := int32(3)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		Status: appsv1.DeploymentStatus{
			ReadyReplicas:     2,
			AvailableReplicas: 2,
		},
	}
	tool := tools.NewKubernetesTool(fake.NewSimpleClientset(dep))

	result, err := tool.Execute(context.Background(), map[string]any{
		"action":    "get_deployments",
		"namespace": "default",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError, result.Error)

	var deps []map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.Content), &deps))
	require.Len(t, deps, 1)
	assert.Equal(t, "api", deps[0]["name"])
	assert.EqualValues(t, 3, deps[0]["desired"])
	assert.EqualValues(t, 2, deps[0]["ready"])
}

func TestKubernetesTool_GetLogs_MissingName(t *testing.T) {
	tool := tools.NewKubernetesTool(fake.NewSimpleClientset())
	result, err := tool.Execute(context.Background(), map[string]any{
		"action": "get_logs",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Error, "requires 'name'")
}
