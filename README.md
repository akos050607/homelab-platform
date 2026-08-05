# homelab-platform

Hybrid Kubernetes platform built as code: Hetzner control plane, remote edge
node over Tailscale, GitOps delivery, identity, observability, policy and a
tested rebuild-from-zero path.

The cluster is torn down by design when not in use and is rebuildable from this
repository. That property is the point — see `docs/adr/` for the decisions and
`docs/CHAOS-LOG.md` for what actually went wrong along the way.

## Status

| Item | State |
|---|---|
| A1 · Hetzner server provisioned as code | done |
| A2 · Hybrid K3s over Tailscale | next |

## A1

CX33 (4 vCPU / 8 GB / 80 GB) in `nbg1`, Ubuntu 24.04, provisioned entirely from
`terraform/`. The machine has never been created or modified in the Hetzner
console; the only manual steps were creating the project and issuing a scoped
API token. Firewall permits 22, 80 and 443 inbound. The Kubernetes API server is
not publicly reachable (ADR-004).

```bash
cd terraform
export HCLOUD_TOKEN="..."
terraform init
terraform apply
```
