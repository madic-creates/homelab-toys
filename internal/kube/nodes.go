package kube

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// NodeStatus is the trimmed view of one Kubernetes Node that callers in
// this repo need: just the name and whether the Ready condition is True.
// Returning a plain struct keeps callers free of corev1 imports.
type NodeStatus struct {
	Name  string
	Ready bool
}

// Nodes lists all cluster Nodes via the given clientset and returns the
// trimmed NodeStatus view. A Node whose Ready condition is missing is
// reported as Ready=false — the conservative interpretation for a
// dashboard signal.
func Nodes(ctx context.Context, cs kubernetes.Interface) ([]NodeStatus, error) {
	list, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	out := make([]NodeStatus, 0, len(list.Items))
	for _, n := range list.Items {
		out = append(out, NodeStatus{Name: n.Name, Ready: nodeReady(n)})
	}
	return out, nil
}

func nodeReady(n corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}
