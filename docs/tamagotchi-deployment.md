# Tamagotchi: Deployment Guide

This document describes how to deploy the `tamagotchi` binary to a Kubernetes
cluster, what it depends on, and the two trip-wires people most often hit.
Ready-to-apply manifests are in [`deploy/tamagotchi/`](../deploy/tamagotchi/);
this guide is the narrative companion.

## What you need

| Resource | Why |
|---|---|
| Namespace | Where the pod runs (`tamagotchi` in the example manifests; any namespace works) |
| Deployment (1 replica) | The `tamagotchi` binary |
| Service (ClusterIP, port 8080) | In-cluster reachability |
| ServiceAccount + ClusterRole + ClusterRoleBinding | `list` on `nodes` and `cert-manager.io/v1 Certificates` cluster-wide; nothing else |
| Role + RoleBinding (namespaced) | `get` on the binary's own pod (for the birthday read at startup) |
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
| `PROMETHEUS_URL` | yes | – | Base URL of the in-cluster Prometheus, e.g. `http://kube-prometheus-stack-prometheus.monitoring.svc.cluster.local:9090`. Used for the node-age and certificate-expiry signals. |
| `ARGOCD_URL` | yes | – | Base URL of the in-cluster argocd-server. **See the HTTP vs HTTPS gotcha below.** |
| `ARGOCD_TOKEN` | yes | – | JWT issued by `argocd account generate-token --account <name>`. Inject from a Secret. |
| `POD_NAME` | yes | – | The pod's own name. Inject via downward API: `fieldRef.fieldPath: metadata.name`. Used to read the pod's creation timestamp for the birthday signal. |
| `POD_NAMESPACE` | yes | – | The pod's own namespace. Inject via downward API: `fieldRef.fieldPath: metadata.namespace`. Used with `POD_NAME` to perform the birthday GET. |

## ArgoCD prerequisites

Tamagotchi calls `GET /api/v1/applications` against the in-cluster ArgoCD API,
authenticating with a bearer token. The API does not accept an arbitrary
ServiceAccount token; you need a real ArgoCD account with at least the
built-in `readonly` role.

Add to the ArgoCD Helm values (or directly to `argocd-cm` and `argocd-rbac-cm`
if you don't use Helm):

```yaml
configs:
  cm:
    accounts.tamagotchi: apiKey
  rbac:
    policy.csv: |
      g, tamagotchi, role:readonly
```

After ArgoCD reconciles, generate a token via the CLI:

```bash
argocd login <argocd-host> --username admin
argocd account generate-token --account tamagotchi
```

Place the JWT into the tamagotchi Secret's `stringData.ARGOCD_TOKEN` field.

> A *project-scoped* JWT is **not** sufficient because the tamagotchi
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

If `tamagotchi` is configured with `ARGOCD_URL=https://...` against an
insecure-mode argocd-server, the Go HTTP client sends a TLS ClientHello to a
server that is not speaking TLS. The server sees garbage on the wire and
resets the connection. Tamagotchi then logs:

```
read tcp <pod-ip>:<port>-><svc-vip>:443: read: connection reset by peer
```

Switching to plain HTTP fixes it. The traffic stays inside the cluster
network, so dropping TLS in this hop is generally acceptable.

If your ArgoCD does **not** use `server.insecure`, then HTTPS works, but the
default cert is self-signed — tamagotchi would either need a CA bundle that
trusts it, or `InsecureSkipVerify`-style behaviour at the client. In-cluster
HTTP is typically the simpler choice.

## Pod birthday and environment variables (the second-most-common trip-wire)

The tamagotchi pet ages relative to the pod's creation timestamp. The binary
reads the pod's metadata at startup via a GET request to the Kubernetes API.
This requires two downward-API environment variables to be set in the
Deployment:

```yaml
env:
  - name: POD_NAME
    valueFrom:
      fieldRef:
        fieldPath: metadata.name
  - name: POD_NAMESPACE
    valueFrom:
      fieldRef:
        fieldPath: metadata.namespace
```

**If these are missing**, the pod's creation timestamp cannot be read, so the
pet starts at age 0 and never ages. The binary logs a WARNING at startup and
continues; the UI will show a permanently young pet.

The reference `deployment.yaml` already sets them, but they must not be
removed if you customize the manifest. The namespaced Role (`tamagotchi-self-pod`)
grants the ServiceAccount permission to GET pods in the deployment's namespace,
which is what enables this read.

## Network policies (the third-most-common trip-wire)

If your cluster runs default-deny network policies (or your CNI implements
implicit deny once any policy selects a pod), three sides must agree on the
allow-list:

1. **tamagotchi egress** — `deploy/tamagotchi/networkpolicy.yaml` covers this:
   DNS to kube-dns, port 9090 to Prometheus, port 8080 to argocd-server,
   plus the kube-apiserver for node and certificate listings (and the self-pod
   birthday read).

2. **argocd-server ingress** — the existing `argocd-server` NetworkPolicy
   needs an additional allow rule for tamagotchi. Adapt to your CNI flavour:

   ```yaml
   ingress:
     # ... existing rules
     - from:
         - namespaceSelector:
             matchLabels:
               kubernetes.io/metadata.name: tamagotchi
           podSelector:
             matchLabels:
               app.kubernetes.io/name: tamagotchi
       ports:
         - port: 8080
           protocol: TCP
   ```

3. **prometheus ingress** — same idea on the Prometheus side, port 9090.

4. **kube-apiserver ingress** — the API server typically allows all in-cluster
   egress by default, but if you have strict egress policies, ensure port 443
   is open from tamagotchi's namespace to the kube-apiserver endpoint.

If you forget any of these, tamagotchi will report
`level=WARN msg="source poll failed" error="... context deadline exceeded"`
for the affected source — symptom-shaped exactly like a missing egress rule
on tamagotchi's side, but the fix might be on the destination app's policy.

The exact label keys and selectors depend on your CNI and how the destination
apps were labelled. Cilium uses `endpointSelector` and `fromEndpoints` with
keys like `k8s:io.kubernetes.pod.namespace`. Standard
`networking.k8s.io/v1 NetworkPolicy` uses `namespaceSelector` + `podSelector`
keyed on `kubernetes.io/metadata.name` (auto-applied by the API server).
Pick whichever your cluster already speaks.

## Probe configuration trade-off (freshness-gated readiness)

Tamagotchi's `/healthz` endpoint returns 503 (Service Unavailable) when data
sources have been stale for >90 seconds. This is by design: the aggregator
should be removed from the Service load-balancer when it can't serve fresh
data.

However, using `/healthz` as the **liveness** probe would restart the pod
during sustained upstream outages (ArgoCD or Prometheus down for >90s). To
avoid unnecessary restarts:

- **Liveness** uses a TCP socket probe on port 8080. This checks only "is the
  process alive and listening?" — the right liveness signal.
- **Readiness** uses `/healthz` so traffic is removed from the Service
  endpoints when the pod can't serve fresh data. The pod stays alive (not
  killed) and recovers automatically once polls succeed.

The reference `deployment.yaml` is configured this way; adjust the probe
timings if your cluster's expected outage patterns differ.

## Ingress

Tamagotchi does not authenticate clients itself. Putting it behind a forward-auth
proxy is the recommended pattern. The example
[`deploy/tamagotchi/ingress.yaml`](../deploy/tamagotchi/ingress.yaml) shows
Traefik with an Authelia middleware; adapt to whatever ingress flavour and
auth proxy your cluster uses (ingress-nginx + oauth2-proxy, HAProxy, etc.).

## Widget URL for dashboards

Tamagotchi exposes a compact `/widget` endpoint that returns self-contained
HTML suitable for embedding into homepage dashboards. For example, if you use
[gethomepage](https://gethomepage.dev/), you can point an iframe widget at:

```
https://<tamagotchi-host>/widget
```

The widget displays the pet's current state and age in a minimal, responsive layout.
This is useful for wall displays and status aggregation pages.

## Verification

After applying the manifests, the pod should reach `Running` quickly:

```bash
kubectl -n <namespace> get pod -l app.kubernetes.io/name=tamagotchi
```

Tail the logs to confirm sources poll cleanly:

```bash
kubectl -n <namespace> logs -l app.kubernetes.io/name=tamagotchi --tail=50 -f
```

A healthy pod logs `listening` once at startup and then nothing else (sources
poll silently on success). Any `level=WARN msg="source poll failed"` entries
name the failing source and the underlying error; those are the breadcrumbs
to debug network policies and tokens.

Hit the JSON state endpoint to see live data:

```bash
kubectl -n <namespace> port-forward svc/tamagotchi 8080:8080
curl -s http://localhost:8080/api/state | jq
```

If `/api/state` returns data but the browser shows an empty page, inspect
the rendered HTML in the browser dev-tools — the page client-side polls
`/api/state` every 30 seconds; broken requests are visible in the network tab.

If the pod logs a WARNING about missing `POD_NAME` or `POD_NAMESPACE` at
startup, verify those downward-API env vars are set in the Deployment and
that the tamagotchi ServiceAccount's namespaced Role allows `get` on pods.

## Topology

```
                    +-----------------+
                    | Ingress + auth  |
                    | (Traefik/etc.)  |
                    +--------+--------+
                             |
                             v
+-----------+  +-----+  +-----+--------+    +-----------------+
| Nodes     |  | Pod |  |  tamagotchi  |    |   Prometheus    |
|(kube-api)|--| GET |--|     pod      |--->| (node-age data, |
|          |  |(self)|  +-----+--------+    |  cert-expiry)   |
+-----------+  +-----+        |             +-----------------+
         |                    v
         |            +-----+--------+
         +----------->| argocd-server|
          (list nodes,|   :8080 HTTP |
           list certs)+-----+--------+
                             ^
                             |
                    (cert-manager)
```

The tamagotchi aggregator depends on three external sources:

- **Kubernetes API** (kube-apiserver): list nodes (node-age calculation), list
  cert-manager Certificates (cert-expiry calculation), and GET the pod's own
  metadata (birthday / age calculation).
- **Prometheus**: query instant metrics for node age and certificate expiry.
- **ArgoCD**: list applications (application health, sync status).

All three polls are wrapped in backoff/retry logic; transient failures do not
crash the pod and are logged at WARN level with the source name and error details.
