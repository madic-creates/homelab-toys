# Cluster-TV: Deployment Guide

This document describes how to deploy the `cluster-tv` binary to a Kubernetes
cluster, what it depends on, and the two trip-wires people most often hit.
Ready-to-apply manifests are in [`deploy/cluster-tv/`](../deploy/cluster-tv/);
this guide is the narrative companion.

## What you need

| Resource | Why |
|---|---|
| Namespace | Where the pod runs (`cluster-tv` in the example manifests; any namespace works) |
| Deployment (1 replica) | The `cluster-tv` binary |
| Service (ClusterIP, port 8080) | In-cluster reachability |
| ServiceAccount + ClusterRole + ClusterRoleBinding | `list` on `cert-manager.io/v1 Certificates` cluster-wide; nothing else |
| Secret with `ARGOCD_TOKEN` | Bearer token for ArgoCD API calls |
| NetworkPolicy | Ingress controller in, DNS / Prometheus / argocd-server / kube-API out |
| Ingress (typically auth-protected) | Browser access |
| ArgoCD local user with `readonly` role | The token must come from a real account |

The pod is fully stateless: no PVC, no leader election, no sidecars.
`Recreate` rollout strategy is sufficient because there is only ever one
replica.

## Environment variables

The binary reads its configuration from environment variables only.

| Variable | Required | Default | Notes |
|---|---|---|---|
| `PORT` | no | `8080` | TCP listen port for the HTTP server |
| `PROMETHEUS_URL` | yes | – | Base URL of the in-cluster Prometheus, e.g. `http://kube-prometheus-stack-prometheus.monitoring.svc.cluster.local:9090`. Used by both the Longhorn-volume signal and the pod-restart signal. |
| `ARGOCD_URL` | yes | – | Base URL of the in-cluster argocd-server. **See the HTTP vs HTTPS gotcha below.** |
| `ARGOCD_TOKEN` | yes | – | JWT issued by `argocd account generate-token --account <name>`. Inject from a Secret. |

## ArgoCD prerequisites

Cluster-TV calls `GET /api/v1/applications` against the in-cluster ArgoCD API,
authenticating with a bearer token. The API does not accept an arbitrary
ServiceAccount token; you need a real ArgoCD account with at least the
built-in `readonly` role.

Add to the ArgoCD Helm values (or directly to `argocd-cm` and `argocd-rbac-cm`
if you don't use Helm):

```yaml
configs:
  cm:
    accounts.cluster-tv: apiKey
  rbac:
    policy.csv: |
      g, cluster-tv, role:readonly
```

After ArgoCD reconciles, generate a token via the CLI:

```bash
argocd login <argocd-host> --username admin
argocd account generate-token --account cluster-tv
```

Place the JWT into the cluster-tv Secret's `stringData.ARGOCD_TOKEN` field.

> A *project-scoped* JWT is **not** sufficient because the cluster-tv
> aggregator counts applications across every project. The role binding above
> grants the account read access to every application via the default
> `readonly` policy.

## HTTP vs HTTPS for ArgoCD (the most common deployment trip-wire)

The ArgoCD Helm chart's default `argocd-cmd-params-cm` ConfigMap contains
`server.insecure: "true"`. With that flag, `argocd-server` listens on its pod
port (8080) in **plain HTTP only** — no TLS at all. The Service still exposes
both port 80 and port 443, but both target containerPort 8080.

Confirm what your cluster does:

```bash
kubectl -n argocd get cm argocd-cmd-params-cm -o jsonpath='{.data.server\.insecure}'
```

If the value is `"true"`:

```yaml
- name: ARGOCD_URL
  value: "http://argocd-server.argocd.svc.cluster.local"
```

If `cluster-tv` is configured with `ARGOCD_URL=https://...` against an
insecure-mode argocd-server, the Go HTTP client sends a TLS ClientHello to a
server that is not speaking TLS. The server sees garbage on the wire and
resets the connection. Cluster-tv then logs:

```
read tcp <pod-ip>:<port>-><svc-vip>:443: read: connection reset by peer
```

Switching to plain HTTP fixes it. The traffic stays inside the cluster
network, so dropping TLS in this hop is generally acceptable.

If your ArgoCD does **not** use `server.insecure`, then HTTPS works, but the
default cert is self-signed — cluster-tv would either need a CA bundle that
trusts it, or `InsecureSkipVerify`-style behaviour at the client. In-cluster
HTTP is typically the simpler choice.

## Network policies (the second-most-common trip-wire)

If your cluster runs default-deny network policies (or your CNI implements
implicit deny once any policy selects a pod), three sides must agree on the
allow-list:

1. **cluster-tv egress** — `deploy/cluster-tv/networkpolicy.yaml` covers this:
   DNS to kube-dns, port 9090 to Prometheus, port 8080 to argocd-server,
   plus the kube-apiserver for cert-manager listings.

2. **argocd-server ingress** — the existing `argocd-server` NetworkPolicy
   needs an additional allow rule for cluster-tv. Adapt to your CNI flavour:

   ```yaml
   ingress:
     # ... existing rules
     - from:
         - namespaceSelector:
             matchLabels:
               kubernetes.io/metadata.name: cluster-tv
           podSelector:
             matchLabels:
               app.kubernetes.io/name: cluster-tv
       ports:
         - port: 8080
           protocol: TCP
   ```

3. **prometheus ingress** — same idea on the Prometheus side, port 9090.

If you forget step 2 or 3, cluster-tv will report
`level=WARN msg="source poll failed" error="... context deadline exceeded"`
for the affected source — symptom-shaped exactly like a missing egress rule
on cluster-tv's side, but the fix is on the destination app's policy.

The exact label keys and selectors depend on your CNI and how the destination
apps were labelled. Cilium uses `endpointSelector` and `fromEndpoints` with
keys like `k8s:io.kubernetes.pod.namespace`. Standard
`networking.k8s.io/v1 NetworkPolicy` uses `namespaceSelector` + `podSelector`
keyed on `kubernetes.io/metadata.name` (auto-applied by the API server).
Pick whichever your cluster already speaks.

## Ingress

Cluster-TV does not authenticate clients itself. Putting it behind a forward-auth
proxy is the recommended pattern. The example
[`deploy/cluster-tv/ingress.yaml`](../deploy/cluster-tv/ingress.yaml) shows
Traefik with an Authelia middleware; adapt to whatever ingress flavour and
auth proxy your cluster uses (ingress-nginx + oauth2-proxy, HAProxy, etc.).

## Verification

After applying the manifests, the pod should reach `Running` quickly:

```bash
kubectl -n <namespace> get pod -l app.kubernetes.io/name=cluster-tv
```

Tail the logs to confirm sources poll cleanly:

```bash
kubectl -n <namespace> logs -l app.kubernetes.io/name=cluster-tv --tail=50 -f
```

A healthy pod logs `listening` once at startup and then nothing else (sources
poll silently on success). Any `level=WARN msg="source poll failed"` entries
name the failing source and the underlying error; those are the breadcrumbs
to debug network policies and tokens.

Hit the JSON state endpoint to see live data:

```bash
kubectl -n <namespace> port-forward svc/cluster-tv 8080:8080
curl -s http://localhost:8080/api/state | jq
```

If `/api/state` returns data but the browser shows an empty page, inspect
the rendered HTML in the browser dev-tools — the page client-side polls
`/api/state` every 30 seconds; broken requests are visible in the network tab.

## Topology

```
                    +-----------------+
                    | Ingress + auth  |
                    | (Traefik/etc.)  |
                    +--------+--------+
                             |
                             v
+-----------------+    +-----+--------+    +-----------------+
| cert-manager    |<---|  cluster-tv  |--->|   Prometheus    |
| Certificates    |    |     pod      |    | (Longhorn vols, |
| (kube-apiserver)|    +-----+--------+    |  pod restarts)  |
+-----------------+          |             +-----------------+
                             v
                       +-----+--------+
                       | argocd-server|
                       |   :8080 HTTP |
                       +--------------+
```

Three of the four signals (Longhorn volume state, pod-restart deltas, and
the implicit health of Prometheus itself) all flow through the single
Prometheus dependency. ArgoCD and the Kubernetes API are dedicated egress
targets.
