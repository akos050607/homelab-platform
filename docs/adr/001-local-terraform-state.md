# ADR-001 — Terraform state stays local

**Context.** Terraform needs somewhere to record the mapping between HCL and real
resources; this repo is public and operated by one person.

**Decision.** State lives on the workstation and is gitignored, along with
`*.tfvars` and `.terraform/`.

**Alternatives rejected.** A remote backend with state locking (S3-compatible
object storage or Terraform Cloud) — correct for a team, but it adds a bootstrap
dependency and a second credential for a single-operator homelab.

**Consequences.** No concurrent `apply` protection, and losing the workstation
means importing resources by hand. Acceptable now; revisit if A9's rebuild has to
run from anywhere other than this machine.
