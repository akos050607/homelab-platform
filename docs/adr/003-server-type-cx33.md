# ADR-003 — CX33 over CX23

**Context.** The build eventually runs kube-prometheus-stack (~2 GB), Loki
(~0.7 GB), Keycloak on the JVM (~1 GB), CloudNativePG (~0.4 GB), ArgoCD (~0.7 GB),
plus ingress-nginx, cert-manager and Kyverno (~0.5 GB) on top of K3s and the OS
(~1 GB) — roughly 6.3 GB steady state before any workload of my own.

**Decision.** CX33: 4 vCPU / 8 GB / 80 GB, €10.78/month.

**Alternatives rejected.** CX23 (4 GB, €6.97) — insufficient headroom, and the
40 GB disk is tight once the Prometheus TSDB, Loki chunks and image cache land.
CAX21 (ARM, cheaper) — rejected because the CI pipeline in Project C builds
amd64 images on GitHub Actions runners, and multi-arch buildx was not a
complexity worth taking on in the first session.

**Consequences.** €3.81/month more than the minimum viable option, in exchange
for not debugging OOMKills that present as unrelated application errors. Over
the expected lifetime of the cluster before teardown, roughly €8 total.
