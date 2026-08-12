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

## 2026-08-07 — A4, GitOps with ArgoCD
- Goal: Transition cluster management from push-based imperative commands to pull-based continuous reconciliation.
- Architecture: Installed ArgoCD in the `argocd` namespace and deployed the "App of Apps" pattern, pointing a root application at a dedicated `homelab-gitops` repository (`apps/` directory).
- Adoption: Migrated the manual `test-app` deployment into declarative Git tracking under `homelab-gitops/apps/test-app.yaml`, allowing ArgoCD to assume ownership via live adoption.
- Drift Correction Demo: Executed a manual out-of-band change (`kubectl scale deployment test-app --replicas=3`). Within the sync window, ArgoCD detected the state drift against the Git declaration and automatically self-healed the deployment back to 1 replica.
- A4 done. Continuous reconciliation verified.
## 2026-08-07 — A5, Secrets Management (Sealed Secrets)
- Goal: Implement GitOps-friendly secrets management to safely commit encrypted credentials to a public repository.
- Architecture: Bitnami Sealed Secrets controller installed and pinned to the `k3s-server` node. Local encryption handled via the `kubeseal` CLI tool.
- Incident 1: `helm repo add` and `wget` for the `kubeseal` binary returned `404 Not Found`.
  - Cause: Bitnami migrated their GitHub repositories and Helm paths from `bitnami-labs/` to `bitnami/`, breaking legacy release URLs.
  - Fix: Updated the Helm repository pointer and the GitHub release download script to target the new organization paths.
- Incident 2: ArgoCD UI abruptly dropped connection with `ERR_CONNECTION_REFUSED` while attempting to verify the GitOps sync.
  - Cause: Switching context/workspaces in VSCode closed the active terminal running the `kubectl port-forward` process, severing the local tunnel to the cluster.
  - Fix: Re-established the port-forward in a dedicated, persistent terminal tab.
- Result: Created a plaintext secret, encrypted it offline into a `SealedSecret` manifest, and destroyed the plaintext file. Pushed the ciphertext to the `homelab-gitops` repository. ArgoCD detected the state change, synced the `SealedSecret`, and the in-cluster controller successfully decrypted it back into a native Kubernetes `Secret`.
- A5 done. Zero plaintext credentials exist in version control.
## 2026-08-08 — Cross-node pod networking dead after edge node reboot

**Symptom.** Both nodes `Ready`, k3s-agent active, tailnet passing traffic
(tx 5.0 MB / rx 11.3 MB), ArgoCD Synced. Pod-to-pod across nodes: 100% loss
both directions, but failing *differently*:

- cloud → edge (10.42.1.79): silent, no ICMP response
- edge → cloud (10.42.0.49): `Destination Host Unreachable` from 10.42.1.1

An immediate local ICMP error means the kernel resolved the destination to an
interface and found nothing there — routing table, not packet filter.

**Hypothesis 1 (wrong).** Tailscale route approval lost on reboot.
`tailscale debug prefs` showed both nodes advertising their own pod CIDR with
`RouteAll: true`. Approval intact.

**Hypothesis 2 (wrong, instructively).** `ip route show` on the server showed no
10.42.1.0/24. False negative — Tailscale installs subnet routes into policy
routing **table 52**, not main. `ip route show table 52` showed it present the
whole time. `ip rule show` confirms the lookup order.

**Actual cause.** The edge node was running a NetworkManager shared connection
(WiFi hotspot). `ipv4.method=shared` defaults to **10.42.0.0/24** — identical to
the pod subnet k3s allocated to the server from its default cluster CIDR of
10.42.0.0/16.

$ ip -4 addr show wlo1
inet 10.42.0.1/24 brd 10.42.0.255 scope global noprefixroute wlo1
$ ip route get 10.42.0.49
10.42.0.49 dev wlo1 src 10.42.0.1


On-link kernel routes resolve before policy routing is consulted, so every packet
for a server-side pod went out the WiFi interface, ARP'd for a host that doesn't
exist, and was dropped locally. One broken route produced two different-looking
failures.

**Fix.** Disabled the shared connection. `wlo1` returned to DHCP (192.168.1.123).

3 packets transmitted, 3 received, 0% packet loss
rtt min/avg/max/mdev = 18.464/18.706/18.890/0.178 ms


18.5 ms Pellérd ↔ Nuremberg — the standing latency cost of the hybrid topology.

**Lessons.**
- Tailscale subnet routes live in table 52. `ip route show` doesn't just omit
  them, it actively misleads.
- k3s defaults to 10.42.0.0/16; NetworkManager shares on 10.42.0.0/24. A hybrid
  cluster is where that surfaces, because one node sits on a network you didn't
  design.
- `kubectl get nodes` proves node reachability, not pod-CIDR route propagation.

---

## 2026-08-10 — ArgoCD reported Synced against a stale revision

A pushed SealedSecret never appeared; Grafana sat in `CreateContainerConfigError`
waiting for it. ArgoCD showed `Synced / Healthy` throughout.

$ kubectl get app root-app -n argocd -o jsonpath='{.status.sync.revision}'
e9dbbd8... # git HEAD was 19754df — two commits ahead


"Synced" means synced to the revision ArgoCD last fetched, not the tip of the
branch. Fixed with `kubectl annotate app root-app -n argocd
argocd.argoproj.io/refresh=hard --overwrite`; a queued sync alone didn't
invalidate the repo cache.

**Lesson.** Health status is not freshness. When a pushed change doesn't appear,
compare `.status.sync.revision` to `git rev-parse HEAD` before debugging the
manifest.

---

## 2026-08-10 — HPA stuck at `<unknown>/50%` for two days

Both `demo` pods had correct CPU requests and `kubectl top pods` worked, so
metrics-server was fine. `kubectl describe hpa` named the cause:

ScalingActive False FailedGetResourceMetric
missing request for cpu in container web of Pod demo-app-694c8f5487-tvq8v


A stale `demo-app` Deployment from five days earlier shared the `app=demo` label
and had no CPU request. Utilisation is computed across **every pod matching the
selector**, not just the target Deployment's pods. One unconfigured pod
invalidated the whole calculation. Deleted it; HPA read `1%/50%` within 30s.

**Lesson.** The symptom points at metrics infrastructure; the cause was a label
collision. Read `describe hpa` first — the condition names the offending pod.

---

## 2026-08-12 — ArgoCD selfHeal and the HPA fighting over spec.replicas

Under load the HPA scaled correctly to 8. At idle, with CPU at 1–2%, the replica
count oscillated:

[11:54:33] cpu: 1%/50% 8
[11:55:03] cpu: 2%/50% 2
[11:55:18] cpu: 1%/50% 4
[11:55:33] cpu: 1%/50% 8


`apps/demo-hpa.yaml` declared `replicas: 2` and root-app runs `selfHeal: true`.
Both controllers own the field: git says 2, HPA says 8, ArgoCD reverts, HPA
scales up, repeat. `root-app` showed `OutOfSync` while the HPA held 8.

**Fix.** Removed `replicas` from the Deployment entirely. Kubernetes defaults it
to 1 at creation and the HPA immediately raises it to `minReplicas`; with nothing
declared, ArgoCD has nothing to compare.

**Lesson.** Silent failure — nothing errors, no pod crashes, the service stays up,
the replica count just stops corresponding to load. Concrete answer to "what's a
downside of GitOps?": declarative reconciliation and dynamic controllers can both
claim the same field. One writer per field.