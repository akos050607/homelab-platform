# ADR-002 — Dedicated Hetzner project for the platform

**Context.** The Hetzner account holds unrelated personal resources, and an API
token is scoped to a project rather than to individual resources.

**Decision.** A project named `homelab-infra` holds this platform and nothing
else. The Read & Write token is issued inside it.

**Alternatives rejected.** Reusing the existing default project — one token would
then carry destroy rights over unrelated machines.

**Consequences.** A mistaken `terraform destroy`, a leaked token, or a bad plan
can only reach resources belonging to this build. One project boundary buys one
blast radius, one state file, and one billing line.
