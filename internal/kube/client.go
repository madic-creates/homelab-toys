// Package kube provides a shared factory for Kubernetes clientsets used by
// the homelab-toys binaries. It centralises the in-cluster config loading
// so that callers do not duplicate the rest.InClusterConfig boilerplate.
package kube

import (
	"fmt"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// NewInCluster loads in-cluster config and returns a typed clientset and a
// dynamic client. Use this from main(); fail fast on error.
func NewInCluster() (kubernetes.Interface, dynamic.Interface, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("in-cluster config: %w", err)
	}
	return NewClientsFromConfig(cfg)
}

// NewClientsFromConfig builds clients from an arbitrary rest.Config. Split
// out so tests (and any future out-of-cluster path) can inject a config.
func NewClientsFromConfig(cfg *rest.Config) (kubernetes.Interface, dynamic.Interface, error) {
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("kubernetes clientset: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("dynamic client: %w", err)
	}
	return cs, dyn, nil
}
