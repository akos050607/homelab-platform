# ADR-005 — DNS records are manual and unproxied

**Context.** The zone `szenassy-akos.com` is hosted on Cloudflare. Cloudflare
records can be proxied (orange cloud) or DNS-only (grey cloud).

**Decision.** Four explicit A records — app, auth, grafana, argocd — all
grey-clouded, created by hand. No wildcard.

**Alternatives rejected.** Proxied records — traffic would terminate at
Cloudflare's edge, so the TLS certificate a visitor sees would be Cloudflare's
rather than the Let's Encrypt certificate cert-manager issues, which is the
artifact this build is meant to earn. Proxying also interferes with the HTTP-01
challenge path and stacks a second reverse proxy in front of ingress-nginx,
doubling the surface for forwarded-header bugs. A wildcard record was rejected
because it would resolve every typo and every unbuilt subdomain to the cluster.

**Consequences.** DNS is the one part of the platform not managed as code, so a
rebuild has a manual step. Managing the zone with the `cloudflare` Terraform
provider is the obvious follow-up and is deliberately deferred, not overlooked.
