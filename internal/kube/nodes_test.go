package kube

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestNodes_AllReady(t *testing.T) {
	cs := fake.NewClientset(
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
			Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			}},
		},
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-2"},
			Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			}},
		},
	)
	got, err := Nodes(context.Background(), cs)
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	for _, n := range got {
		if !n.Ready {
			t.Errorf("%s ready = false, want true", n.Name)
		}
	}
}

func TestNodes_OneNotReady(t *testing.T) {
	cs := fake.NewClientset(
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "ok"},
			Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			}},
		},
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "bad"},
			Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
			}},
		},
	)
	got, err := Nodes(context.Background(), cs)
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	want := map[string]bool{"ok": true, "bad": false}
	for _, n := range got {
		if w, ok := want[n.Name]; !ok || w != n.Ready {
			t.Errorf("%s ready = %v, want %v", n.Name, n.Ready, w)
		}
	}
}

func TestNodes_NoReadyConditionTreatedAsNotReady(t *testing.T) {
	cs := fake.NewClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "weird"}},
	)
	got, err := Nodes(context.Background(), cs)
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(got) != 1 || got[0].Ready {
		t.Fatalf("got %+v, want one not-ready node", got)
	}
}
