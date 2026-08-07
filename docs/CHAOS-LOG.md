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

## 2026-08-06 — A2, Hybrid K3s over Tailscale
- Goal: Securely join a local edge node to a Hetzner control plane.
- Incident 1: K3s failed to start on the Hetzner node. `journalctl` showed `VPN Error. The passed VPN auth info includes an unknown parameter: name\`. 
  - Cause: Bash quote-escaping over SSH mangled the `--vpn-auth` string before systemd could read it.
  - Fix: Abandoned CLI flags entirely. Wrote a declarative `/etc/rancher/k3s/config.yaml` to bypass shell parsing bugs (See ADR-006).
- Incident 2: K3s still crashed. `tailscale: command not found`.
  - Cause: K3s's native Tailscale integration assumes the daemon is already on the host OS; it does not bootstrap it. 
  - Fix: Ran the Tailscale install script on the host OS as a prerequisite.
- Incident 3: Spawning the test pod on the edge node failed with `Invalid value: "akos050607-Thin-GF63-12VE": a lowercase RFC 1123 subdomain must consist of lower case alphanumeric characters`.
  - Cause: The laptop's hostname had uppercase letters. K3s quietly converted it to lowercase when registering the node, but the explicit `nodeName` override in the test pod command didn't match.
  - Fix: Piped `$(hostname)` through `tr '[:upper:]' '[:lower:]'` to match Kubernetes' strict naming requirements.
- Result: Nodes registered as `Ready`. Cross-node pod ping over the Tailscale overlay succeeded with 0% packet loss. A2 done.

## 2026-08-07 — A3, TLS ingress + cert-manager
- Goal: Secure the cluster front door with automated Let's Encrypt certificates.
- Incident 1: `helm install cert-manager` stalled infinitely.
  - Cause: Webhook pod randomly scheduled onto the edge node (laptop). The Kubernetes API server (on the Hetzner node) tried to validate the installation across the Tailscale VPN, but the traffic vanished into a black hole.
  - Fix: Cancelled the install, wiped the broken state, and used `--set nodeSelector`, `webhook.nodeSelector`, and `cainjector.nodeSelector` to strictly pin all cert-manager components to the `k3s-server`.
- Incident 2: Let's Encrypt HTTP-01 challenge timed out with `context deadline exceeded`.
  - Diagnostics: `curl localhost:80` returned a 404 (K3s listening normally), and `ufw status` was inactive. Hetzner Cloud UI confirmed the external firewall was wide open on 80/443.
  - Cause: The `externalTrafficPolicy: Local` setting on the ingress service. Kube-proxy saw traffic hitting the public `eth0` interface, but the ingress pod was bound to the `tailscale0` VPN interface. Kubernetes incorrectly concluded the pod was non-local and issued an iptables `REJECT`.
  - Fix: Hot-patched the service to `externalTrafficPolicy: Cluster`.
- Incident 3: Curling the domain locally returned "No route to host" (Hairpin NAT failure), while curling from a 4G mobile network returned a `504 Gateway Time-out`.
  - Cause: Nginx (on the Hetzner node) caught the public request, but the Let's Encrypt `cm-acme-http-solver` pod had scheduled onto the edge node. The A2 cross-node Tailscale routing silently dropped the forwarded packet. 
  - Fix: Executed `kubectl cordon akos050607-thin-gf63-12ve` to temporarily sideline the edge node, then evicted the pods. They rescheduled onto the Hetzner server, Nginx routed the traffic over `localhost`, and the production certificate issued immediately. 
- A3 done. Green padlock verified on mobile. (Note: A2 Tailscale pod-to-pod routing remains fundamentally broken and requires a dedicated debugging sprint).