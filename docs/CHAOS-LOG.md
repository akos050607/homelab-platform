# Chaos log
## 2026-08-05
- 20:14 — Starting A1. Nothing provisioned yet. Goal: server exists only as HCL.

## 2026-08-05 — A1, Hetzner provisioned as code

- 20:14 — Started. Nothing provisioned. Goal: server exists only as HCL.
- Hetzner project `homelab-infra` + scoped R/W token created by hand. Only two
  manual actions in the whole item; everything else is Terraform.
- `terraform apply` → `Error: server type cx32 not found`. SSH key and firewall
  created successfully first, so only the server resource failed.
  - Wrong hypothesis: bad provider version or a region availability issue.
  - Actual cause: Hetzner renamed the CX line a generation up. CX22/CX32 are now
    CX23/CX33. The plan document was written against the old names.
  - Found by querying `/v1/server_types` directly instead of guessing a replacement.
- Sizing decision: CX23 (4 GB) rejected, CX33 (8 GB) chosen. See ADR-003.
- Firewall verified from outside: `ss -tlnp` on the host shows only :22 public
  (the two :53 entries are systemd-resolve on loopback). `nc -zv <ip> 6443` hangs
  with no output rather than returning "connection refused" — the firewall DROPs
  rather than REJECTs, so the port is silent to a scanner instead of advertising
  that something is filtering. Observed, not read.
- DNS: app / auth / grafana / argocd A records in Cloudflare, all four grey-clouded
  (DNS only). See ADR-005.
- A1 done. Server was never touched in the Hetzner UI; `terraform state list`
  confirms all three resources are managed.
