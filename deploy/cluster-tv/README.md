# Cluster-TV: Example Kubernetes Manifests

Minimal, ready-to-deploy manifests for `cluster-tv`. Each file is independent
and self-documenting; comment headers explain what to adjust. For the full
deployment narrative, prerequisites, and common pitfalls, see
[`docs/cluster-tv-deployment.md`](../../docs/cluster-tv-deployment.md).

## What's here

| File | Purpose | Required |
|---|---|---|
| `namespace.yaml` | Dedicated `cluster-tv` namespace | Optional — drop if reusing an existing namespace |
| `rbac.yaml` | ServiceAccount + ClusterRole + ClusterRoleBinding (only `list` on cert-manager Certificates) | Yes |
| `secret.yaml` | `cluster-tv-env` Secret with the ArgoCD bearer token | Yes — replace the placeholder before applying |
| `deployment.yaml` | The pod, with env vars and probes | Yes |
| `service.yaml` | ClusterIP on port 8080 | Yes |
| `networkpolicy.yaml` | Standard NetworkPolicy for ingress + egress | Recommended |
| `ingress.yaml` | Browser-facing Ingress (Traefik + Authelia example) | Optional |

## Quick start

```bash
# 1. Create an ArgoCD account (in argocd-cm) and generate a token
#    (see ../../docs/cluster-tv-deployment.md for the full procedure)
TOKEN=$(argocd account generate-token --account cluster-tv)

# 2. Apply the manifests
kubectl apply -f namespace.yaml
kubectl apply -f rbac.yaml
sed "s|REPLACE_WITH_ARGOCD_LOCAL_USER_TOKEN|$TOKEN|" secret.yaml | kubectl apply -f -
kubectl apply -f deployment.yaml
kubectl apply -f service.yaml
kubectl apply -f networkpolicy.yaml
# Optional, if you want a public-facing host:
kubectl apply -f ingress.yaml
```

## What you almost certainly need to change

- **`PROMETHEUS_URL`** in `deployment.yaml` — defaults to the kube-prometheus-stack
  service name; adjust if you use a different Helm release name or chart.
- **`ARGOCD_URL`** in `deployment.yaml` — defaults to plain HTTP because the
  upstream ArgoCD Helm chart sets `server.insecure: "true"` by default. If
  your ArgoCD serves TLS, switch to `https://` and ensure the binary trusts
  the cert (or use a side-loaded ArgoCD certificate).
- **Image tag** in `deployment.yaml` — uses `:latest` for clarity. Pin to a
  specific digest in production (`ghcr.io/madic-creates/cluster-tv@sha256:...`)
  and let your dependency-update bot move it forward.
- **NetworkPolicy** in `networkpolicy.yaml` — namespace label keys and pod
  labels must match your cluster's actual labels. The destination apps
  (argocd-server, prometheus) usually need their own ingress policies updated
  to accept traffic from `cluster-tv`; see the deployment doc.
- **Ingress** in `ingress.yaml` — the Traefik/Authelia annotations are an
  example. Replace with your own ingress flavour, or drop the file entirely
  and put cluster-tv behind whatever forward-auth you already use.

## Verifying

```bash
kubectl -n cluster-tv get pod -l app.kubernetes.io/name=cluster-tv
kubectl -n cluster-tv logs -l app.kubernetes.io/name=cluster-tv --tail=50
kubectl -n cluster-tv port-forward svc/cluster-tv 8080:8080
# then: curl -s http://localhost:8080/api/state | jq
```

A healthy pod logs a single `listening` line. `level=WARN msg="source poll
failed"` lines are the troubleshooting breadcrumbs — see the deployment doc
for what each source needs.
