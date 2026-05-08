package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/ipiton/AMP/internal/core/investigation"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// KubernetesTool provides Kubernetes diagnostic actions for the investigation agent.
// It accepts a kubernetes.Interface so tests can inject a fake client.
type KubernetesTool struct {
	clientset kubernetes.Interface
}

// NewKubernetesTool creates a tool backed by the given k8s clientset.
func NewKubernetesTool(clientset kubernetes.Interface) *KubernetesTool {
	return &KubernetesTool{clientset: clientset}
}

// NewKubernetesToolFromConfig creates a tool by building a real k8s client.
// kubeconfig path may be empty (in-cluster) or a path to a kubeconfig file.
func NewKubernetesToolFromConfig(kubeconfig string) (*KubernetesTool, error) {
	cs, err := buildClientset(kubeconfig)
	if err != nil {
		return nil, err
	}
	return &KubernetesTool{clientset: cs}, nil
}

func buildClientset(kubeconfig string) (kubernetes.Interface, error) {
	var cfg *rest.Config
	var err error
	if kubeconfig == "" {
		cfg, err = rest.InClusterConfig()
	} else {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	if err != nil {
		return nil, fmt.Errorf("kubernetes: build config: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: create clientset: %w", err)
	}
	return cs, nil
}

func (t *KubernetesTool) Definition() investigation.ToolDefinition {
	return investigation.ToolDefinition{
		Name:        "kubernetes_action",
		Description: "Perform Kubernetes diagnostic actions: list pods, get pod status, fetch events, read container logs, or list deployments.",
		Parameters: investigation.JSONSchemaObject{
			Type: "object",
			Properties: map[string]investigation.JSONSchemaField{
				"action": {
					Type:        "string",
					Description: "Action to perform: list_pods, get_pod, get_events, get_logs, get_deployments",
				},
				"namespace": {
					Type:        "string",
					Description: "Kubernetes namespace (default: default)",
					Default:     "default",
				},
				"name": {
					Type:        "string",
					Description: "Pod or deployment name (required for get_pod and get_logs)",
				},
				"container": {
					Type:        "string",
					Description: "Container name for get_logs (optional, uses first container if omitted)",
				},
				"tail_lines": {
					Type:        "string",
					Description: "Number of tail log lines for get_logs (default: 100)",
					Default:     "100",
				},
				"label_selector": {
					Type:        "string",
					Description: "Label selector for list_pods or get_events, e.g. app=myservice",
				},
			},
			Required: []string{"action"},
		},
	}
}

func (t *KubernetesTool) Execute(ctx context.Context, params map[string]any) (investigation.ToolResult, error) {
	action, _ := params["action"].(string)
	if action == "" {
		return investigation.ToolResult{IsError: true, Error: "kubernetes: missing required param 'action'"}, nil
	}

	namespace := stringParam(params["namespace"], "default")

	switch action {
	case "list_pods":
		return t.listPods(ctx, namespace, stringParam(params["label_selector"], ""))
	case "get_pod":
		return t.getPod(ctx, namespace, stringParam(params["name"], ""))
	case "get_events":
		return t.getEvents(ctx, namespace, stringParam(params["name"], ""), stringParam(params["label_selector"], ""))
	case "get_logs":
		return t.getLogs(ctx, namespace, stringParam(params["name"], ""),
			stringParam(params["container"], ""), stringParam(params["tail_lines"], "100"))
	case "get_deployments":
		return t.getDeployments(ctx, namespace, stringParam(params["label_selector"], ""))
	default:
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("kubernetes: unknown action %q", action)}, nil
	}
}

func (t *KubernetesTool) listPods(ctx context.Context, namespace, labelSelector string) (investigation.ToolResult, error) {
	list, err := t.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("kubernetes: list_pods: %v", err)}, nil
	}
	items := make([]podSummary, 0, len(list.Items))
	for i := range list.Items {
		items = append(items, summarizePod(&list.Items[i]))
	}
	return marshalResult(items)
}

func (t *KubernetesTool) getPod(ctx context.Context, namespace, name string) (investigation.ToolResult, error) {
	if name == "" {
		return investigation.ToolResult{IsError: true, Error: "kubernetes: get_pod requires 'name'"}, nil
	}
	pod, err := t.clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("kubernetes: get_pod: %v", err)}, nil
	}
	return marshalResult(summarizePod(pod))
}

func (t *KubernetesTool) getEvents(ctx context.Context, namespace, involvedObject, fieldSelector string) (investigation.ToolResult, error) {
	opts := metav1.ListOptions{}
	if involvedObject != "" {
		opts.FieldSelector = fmt.Sprintf("involvedObject.name=%s", involvedObject)
	} else if fieldSelector != "" {
		opts.LabelSelector = fieldSelector
	}
	list, err := t.clientset.CoreV1().Events(namespace).List(ctx, opts)
	if err != nil {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("kubernetes: get_events: %v", err)}, nil
	}
	items := make([]eventSummary, 0, len(list.Items))
	for i := range list.Items {
		items = append(items, summarizeEvent(&list.Items[i]))
	}
	return marshalResult(items)
}

func (t *KubernetesTool) getLogs(ctx context.Context, namespace, name, container, tailLines string) (investigation.ToolResult, error) {
	if name == "" {
		return investigation.ToolResult{IsError: true, Error: "kubernetes: get_logs requires 'name'"}, nil
	}
	var tail *int64
	if n, err := parseIntParam(tailLines, 100); err == nil {
		tail = &n
	}
	opts := &corev1.PodLogOptions{
		Container: container,
		TailLines: tail,
	}
	req := t.clientset.CoreV1().Pods(namespace).GetLogs(name, opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("kubernetes: get_logs: %v", err)}, nil
	}
	defer stream.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, stream); err != nil {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("kubernetes: get_logs read: %v", err)}, nil
	}
	out, err := json.Marshal(map[string]string{"logs": buf.String()})
	if err != nil {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("kubernetes: get_logs marshal: %v", err)}, nil
	}
	return investigation.ToolResult{Content: string(out)}, nil
}

func (t *KubernetesTool) getDeployments(ctx context.Context, namespace, labelSelector string) (investigation.ToolResult, error) {
	list, err := t.clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("kubernetes: get_deployments: %v", err)}, nil
	}
	items := make([]deploymentSummary, 0, len(list.Items))
	for i := range list.Items {
		items = append(items, summarizeDeployment(&list.Items[i]))
	}
	return marshalResult(items)
}

// Summary structs — compact JSON for LLM consumption.

type podSummary struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Phase     string            `json:"phase"`
	Ready     bool              `json:"ready"`
	Restarts  int32             `json:"restarts"`
	NodeName  string            `json:"node,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

type eventSummary struct {
	Type    string `json:"type"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Object  string `json:"object"`
	Count   int32  `json:"count"`
	Time    string `json:"last_time,omitempty"`
}

type deploymentSummary struct {
	Name       string            `json:"name"`
	Namespace  string            `json:"namespace"`
	Desired    int32             `json:"desired"`
	Ready      int32             `json:"ready"`
	Available  int32             `json:"available"`
	Labels     map[string]string `json:"labels,omitempty"`
	Conditions []string          `json:"conditions,omitempty"`
}

func summarizePod(p *corev1.Pod) podSummary {
	ready := len(p.Status.ContainerStatuses) > 0
	var restarts int32
	for _, cs := range p.Status.ContainerStatuses {
		restarts += cs.RestartCount
		if !cs.Ready {
			ready = false
		}
	}
	return podSummary{
		Name:      p.Name,
		Namespace: p.Namespace,
		Phase:     string(p.Status.Phase),
		Ready:     ready,
		Restarts:  restarts,
		NodeName:  p.Spec.NodeName,
		Labels:    p.Labels,
	}
}

func summarizeEvent(e *corev1.Event) eventSummary {
	var lastTime string
	if !e.LastTimestamp.IsZero() {
		lastTime = e.LastTimestamp.UTC().Format("2006-01-02T15:04:05Z")
	}
	return eventSummary{
		Type:    e.Type,
		Reason:  e.Reason,
		Message: e.Message,
		Object:  fmt.Sprintf("%s/%s", e.InvolvedObject.Kind, e.InvolvedObject.Name),
		Count:   e.Count,
		Time:    lastTime,
	}
}

func summarizeDeployment(d *appsv1.Deployment) deploymentSummary {
	var conditions []string
	for _, c := range d.Status.Conditions {
		conditions = append(conditions, fmt.Sprintf("%s=%s", c.Type, c.Status))
	}
	return deploymentSummary{
		Name:       d.Name,
		Namespace:  d.Namespace,
		Desired:    derefInt32(d.Spec.Replicas),
		Ready:      d.Status.ReadyReplicas,
		Available:  d.Status.AvailableReplicas,
		Labels:     d.Labels,
		Conditions: conditions,
	}
}

func derefInt32(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

func marshalResult(v any) (investigation.ToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("kubernetes: marshal: %v", err)}, nil
	}
	return investigation.ToolResult{Content: string(b)}, nil
}

func parseIntParam(s string, defaultVal int64) (int64, error) {
	if s == "" {
		return defaultVal, nil
	}
	var n int64
	_, err := fmt.Sscanf(s, "%d", &n)
	if err != nil {
		return defaultVal, err
	}
	return n, nil
}
