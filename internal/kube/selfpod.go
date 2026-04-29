package kube

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// SelfPodCreatedAt fetches the pod identified by namespace+name and returns
// its metadata.creationTimestamp. Used at process startup to derive the
// pet's age, which is then cached for the lifetime of the binary.
//
// The downward API exposes POD_NAME and POD_NAMESPACE via fieldRef, but
// not creationTimestamp, so this round-trip is the cleanest path.
func SelfPodCreatedAt(ctx context.Context, cs kubernetes.Interface, namespace, name string) (time.Time, error) {
	pod, err := cs.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return time.Time{}, fmt.Errorf("get self pod %s/%s: %w", namespace, name, err)
	}
	return pod.CreationTimestamp.Time, nil
}
