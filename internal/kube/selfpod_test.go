package kube

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestSelfPodCreatedAt_ReturnsCreationTimestamp(t *testing.T) {
	want := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	cs := fake.NewClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "tama-abc",
			Namespace:         "monitoring",
			CreationTimestamp: metav1.NewTime(want),
		},
	})
	got, err := SelfPodCreatedAt(context.Background(), cs, "monitoring", "tama-abc")
	if err != nil {
		t.Fatalf("SelfPodCreatedAt: %v", err)
	}
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSelfPodCreatedAt_NotFoundReturnsError(t *testing.T) {
	cs := fake.NewClientset()
	_, err := SelfPodCreatedAt(context.Background(), cs, "monitoring", "tama-abc")
	if err == nil {
		t.Fatal("want error, got nil")
	}
}
