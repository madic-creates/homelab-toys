package kube

import (
	"testing"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// TestNewClientsFromConfigSignature is a compile-time assertion that
// NewClientsFromConfig returns the interface types we depend on.
// It does not call rest.InClusterConfig (which only works inside a Pod).
func TestNewClientsFromConfigSignature(t *testing.T) {
	cfg := &rest.Config{Host: "https://example.invalid"}
	cs, dyn, err := NewClientsFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewClientsFromConfig: %v", err)
	}
	var _ kubernetes.Interface = cs
	var _ dynamic.Interface = dyn
}
