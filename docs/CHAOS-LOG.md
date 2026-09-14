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
---

## 2026-09-13 — CI for the GitOps repo: four checks, and what each actually catches

Goal: stop a broken manifest reaching the branch Argo CD syncs. Validation only —
nothing in the pipeline deploys, and no job holds a cluster credential (ADR-007).

**kubeconform sees only the vocabulary you give it.** First run, default schemas:
8 of 14 resources errored — every `ClusterIssuer`, `SealedSecret` and Argo CD
`Application`. Not invalid, *unknown*: CRDs extend the API and the stock schema
set has never heard of them. Wiring the datreeio/CRDs-catalog URL template took
it to 14/14. Checked afterwards that the CRD schemas are genuinely strict rather
than permissive stubs — adding `totallyBogusField` to a SealedSecret is rejected
with `additional properties not allowed`, and deleting `spec.destination` from an
Application is rejected as a missing required property. So the CRs are held to
the same standard as core kinds, which I had assumed but had not verified.

**A linter that cries wolf gets switched off.** `yamllint` with stock defaults
produced 60 findings, 42 of them the `braces` rule objecting to
`selector: { matchLabels: { app: demo } }` — a style used deliberately throughout
the repo. Tuning it to 4 real findings meant deciding, rule by rule, which are
style and which are bug classes. Relaxed: braces, colon alignment, line length,
document-start. Kept strict: indentation (`consistent`, because `test-app.yaml`
and `demo-hpa.yaml` genuinely disagree about sequence indentation), trailing
whitespace, and duplicate keys — a duplicate key is not an error in YAML, the
last one silently wins, which in a manifest means a setting you can read in the
file having no effect. Enabled two rules that are off by default: implicit octal
(`mode: 0644` is not 644) and empty values.

Three of the four survivors were SealedSecret ciphertext — a single 770-character
base64 token that cannot be wrapped and is regenerated by `kubeseal` anyway.
Scoped the line-length rule off those two files rather than raising the global
limit to 800 and losing the rule everywhere.

**The check I nearly did not build turned out to be the only one that works.**
Three manifests are Argo CD Applications wrapping a Helm chart, with the values
as a block scalar — a *string* as far as the YAML is concerned. So yamllint never
parses it and kubeconform sees a perfectly valid string. Breaking
`retention: 3d` to `retention: [3d` inside that block passes lint, validate and
policy, and only fails when something actually renders the chart. Added a
`render` check that pulls chart, repo, version and values out of each Application,
runs `helm template`, and feeds the output back through kubeconform — 133
resources across the three charts.

Then tested what it *doesn't* catch, which turned out to matter more. Helm has no
strict-values mode: `totallyBogusKey: 42` and `replicas: "three"` both render
clean, even against Loki's own `values.schema.json`. So `render` catches values
that break rendering or produce invalid objects, and not misspelled keys the
chart ignores. Wrote that limitation into the README rather than letting the
check look stronger than it is.

It also surfaced something I did not know: `helm template` prints
`WARNING: This chart is deprecated` for promtail. Grafana has moved to Alloy.

**Schema validation is not a security control.** The repo's claim since A5 has
been "zero plaintext credentials in version control", which was a property
maintained by remembering to maintain it. A plaintext `Secret` with base64 `data:`
is completely schema-valid — kubeconform passes it. So the `policy` check tests
the invariant directly with `yq` (matching on `.kind == "Secret"`, so the word
"Secret" inside a *Sealed*Secret is not a false positive) plus a placeholder grep,
the same trick as the `verify:clean` guard on the CV site.

It went red on its first run against two real defects that had been sitting in
the repo: `# REPLACE THIS with your actual GitHub repo URL` in `root-app.yaml`,
and `email: your-actual-email@example.com` in the staging ClusterIssuer.

**Then the linter caught me.** Enabling `empty-values` made `lint` fail on my own
workflow file — `workflow_dispatch:` is a deliberately valueless key that GitHub
Actions requires. Scoped the rule off `.github/workflows/` rather than dropping
it. A rule finding a false positive on day one is a fair demonstration that it is
actually running.

**Proving it rather than asserting it.** Injected eight defects one at a time and
recorded which checks went red. Seven behaved correctly first time; the eighth —
the values-block test, the important one — came back all-green, which for ten
minutes looked like the `render` check not working. It was my test that was
broken: the `sed` pattern used eight spaces of indentation and the line has
twelve, so nothing was ever injected. Fixed the pattern and it went red as
designed. The near-miss is the lesson: a negative test that silently fails to
inject its defect reports the same "all green" as a pipeline with no gaps.

| Injected defect | lint | validate | render | policy |
|---|---|---|---|---|
| trailing whitespace | RED | ok | ok | ok |
| `replicas: "one"` (valid YAML, wrong type) | ok | RED | ok | ok |
| unknown field on a Deployment | ok | RED | ok | ok |
| required `spec.destination` removed (CR) | ok | RED | ok | ok |
| broken YAML inside `helm.values` | ok | ok | RED | ok |
| `targetRevision: "999.*"` | ok | ok | RED | ok |
| plaintext Secret with `stringData` | ok | ok | ok | RED |
| `CHANGEME` placeholder | ok | ok | ok | RED |

**Lessons.**
- Validation covers the vocabulary you hand it. CRDs extend the vocabulary, so
  "it passed" means nothing until you check the schemas actually loaded.
- The failure mode of a gate is not a false alarm, it is silent success. Both the
  linter's 42 false positives and the negative test that injected nothing would
  have ended with a green tick.
- Know the limit of your own check and write it down. `render` is worth having
  *and* cannot catch a misspelled key; those are both true.

## 2026-09-14 — B3 and B4, passkey and SAML
- Goal: passwordless login actually replacing the password step, and a SAML client in the same realm for comparison.
- Built `browser-passwordless` via the admin API: Username Form -> WebAuthn Passwordless Authenticator, both REQUIRED, password form and the conditional-2FA subflow removed. RP ID `auth.szenassy-akos.com`, user verification required, resident key yes.
- Deliberately did NOT bind the new flow until a passkey existed. Binding first would have left the only user in the realm with no way to authenticate at all.
- Incident 5: the realm committed to git was still the bootstrap realm. Everything configured through the admin console — the flow, the WebAuthn policy, the roles mapper, the browser flow binding — existed only in Postgres.
  - Argo CD reported the ConfigMap `Synced` throughout, and was right: the file matched git. Git had simply stopped describing the running realm.
  - Consequence if unnoticed: a rebuild from git produces a Keycloak with no passwordless login, which contradicts the rebuild-from-zero property the platform claims.
  - Fix: `scripts/export-realm.sh` + `scripts/normalise-realm.py`, so re-exporting is one command with a reviewable diff. An export that is a manual clean-up gets done once and then rots.
  - Verified non-destructively by importing the committed file into a throwaway realm `homelab-verify`, confirming it reproduced browserFlow, the WebAuthn policy and both REQUIRED flow steps, then deleting it. The live realm and the registered passkey were never touched.
- Incident 6: after merging the export, Argo CD reported `Synced` **at the correct commit SHA** while the cluster still held the previous 1787-byte ConfigMap. Not a stale revision — the right revision with stale content.
  - Found by checking the object (`kubectl get cm ... | wc -c`) rather than the dashboard. A hard refresh corrected it: 1787 -> 61616 bytes.
  - Worth separating from the earlier stale-revision case: that one sounds like a caching detail. This one reported the right answer to "which commit" and the wrong answer to "what is deployed".
- Incident 7: the SAML `/saml/acs` endpoint pretty-printed assertions by decoding and re-encoding with `encoding/xml`. Output was valid XML and completely wrong — Go's encoder does not preserve namespace prefixes, so `<samlp:Response>` became `<Response xmlns="...">` with an invented `_xmlns:samlp="xmlns"` attribute.
  - This is the canonicalisation problem demonstrated by accident. A SAML signature covers exact bytes; any semantically-equivalent re-serialisation breaks it. Re-serialising with a general-purpose XML library is precisely the classic mistake.
  - Fix: `indentXML` now only inserts whitespace between `>` and `<`, never re-parses, and a test asserts the document is byte-identical once whitespace is stripped.
- Three incidents in two days with the same shape (5, 6, and the orphaned Ingress in ADR-010): a green dashboard says the things being watched are healthy. It does not say that what is present is what was declared.
