// Package certs lists cert-manager Certificate resources cluster-wide via
// the dynamic client. We use the dynamic client (rather than the typed
// cert-manager-io Go module) to keep the homelab-toys dep graph small —
// only status.notAfter is needed, and that field is stable.
package certs

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// Cert is the trimmed view of a cert-manager Certificate.
type Cert struct {
	Namespace string
	Name      string
	NotAfter  time.Time
}

// CertificatesGVR is the GroupVersionResource of cert-manager certificates.
var CertificatesGVR = schema.GroupVersionResource{
	Group:    "cert-manager.io",
	Version:  "v1",
	Resource: "certificates",
}

type Lister struct {
	dyn dynamic.Interface
}

func NewLister(dyn dynamic.Interface) *Lister {
	return &Lister{dyn: dyn}
}

// ExpiringSoon returns certs whose status.notAfter is set and is strictly
// before now+window. Already-expired certs are included (they count as
// "expiring"); certs without a populated status.notAfter are skipped — the
// spec is explicit on this.
func (l *Lister) ExpiringSoon(ctx context.Context, now time.Time, window time.Duration) ([]Cert, error) {
	list, err := l.dyn.Resource(CertificatesGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list certificates: %w", err)
	}

	cutoff := now.Add(window)
	out := make([]Cert, 0, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		t, ok := readNotAfter(item)
		if !ok {
			continue
		}
		if t.Before(cutoff) {
			out = append(out, Cert{
				Namespace: item.GetNamespace(),
				Name:      item.GetName(),
				NotAfter:  t,
			})
		}
	}
	return out, nil
}

func readNotAfter(u *unstructured.Unstructured) (time.Time, bool) {
	s, found, err := unstructured.NestedString(u.Object, "status", "notAfter")
	if err != nil || !found || s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
