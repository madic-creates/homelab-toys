package certs

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func newCert(ns, name string, notAfter *time.Time) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{Group: "cert-manager.io", Version: "v1", Kind: "Certificate"})
	u.SetNamespace(ns)
	u.SetName(name)
	if notAfter != nil {
		_ = unstructured.SetNestedField(u.Object, notAfter.UTC().Format(time.RFC3339), "status", "notAfter")
	}
	return u
}

func TestExpiringSoon(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	in7d := now.Add(7 * 24 * time.Hour)
	in40d := now.Add(40 * 24 * time.Hour)
	expired := now.Add(-2 * 24 * time.Hour)

	scheme := runtime.NewScheme()
	gvr := schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}
	listKinds := map[schema.GroupVersionResource]string{gvr: "CertificateList"}

	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds,
		newCert("default", "soon", &in7d),
		newCert("kube-system", "later", &in40d),
		newCert("monitoring", "expired", &expired),
		newCert("default", "no-notafter", nil),
	)

	l := NewLister(dyn)
	res, err := l.ExpiringSoon(context.Background(), now, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("ExpiringSoon: %v", err)
	}

	got := map[string]bool{}
	for _, c := range res {
		got[c.Namespace+"/"+c.Name] = true
	}
	if !got["default/soon"] {
		t.Errorf("expected default/soon")
	}
	if !got["monitoring/expired"] {
		t.Errorf("expected monitoring/expired (already expired counts as expiring)")
	}
	if got["kube-system/later"] {
		t.Errorf("did not expect kube-system/later (40d > 30d window)")
	}
	if got["default/no-notafter"] {
		t.Errorf("did not expect default/no-notafter (skip cert without notAfter)")
	}
	if len(res) != 2 {
		t.Errorf("len = %d, want 2", len(res))
	}

	for _, c := range res {
		if c.Namespace == "default" && c.Name == "soon" {
			if !c.NotAfter.Equal(in7d) {
				t.Errorf("notAfter for default/soon = %v, want %v", c.NotAfter, in7d)
			}
		}
	}
}

func TestExpiringSoon_EmptyList(t *testing.T) {
	scheme := runtime.NewScheme()
	gvr := schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{gvr: "CertificateList"})
	l := NewLister(dyn)
	res, err := l.ExpiringSoon(context.Background(), time.Now(), 30*24*time.Hour)
	if err != nil {
		t.Fatalf("ExpiringSoon: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("len = %d, want 0", len(res))
	}
}

// silence unused-import warning when go test --short is used
var _ = metav1.ListOptions{}
