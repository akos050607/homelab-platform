# ADR-004 — Kubernetes API server is not reachable from the internet

**Context.** K3s serves the API on 6443. The default instinct is to open it so
`kubectl` works from anywhere.

**Decision.** The firewall permits 22, 80 and 443 inbound only. The edge node
joins the cluster over Tailscale, and administrative `kubectl` traffic uses the
same tailnet.

**Alternatives rejected.** Exposing 6443 publicly, with or without a source-IP
allowlist — an allowlist breaks on any dynamic address, and a public control
plane is a standing authentication surface for no gain here.

**Consequences.** Cluster administration requires tailnet membership, which is
the intended property. The public firewall carries no exception for the control
plane, and port 6443 is silent to an external scanner.
