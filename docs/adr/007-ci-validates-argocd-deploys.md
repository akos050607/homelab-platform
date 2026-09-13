# ADR-007 — CI validates, Argo CD deploys

**Context.** `homelab-gitops` is reconciled by Argo CD with `automated` sync,
`prune: true` and `selfHeal: true`. Anything reaching `main` is applied to the
cluster within minutes, and `selfHeal` means a hand-edit cannot undo it — Argo CD
puts it back. Until now the repository had no CI, no branch protection and a
single branch, so a typo in a manifest was a deployment. The obvious instinct,
and what most CV bullet points mean by "CI/CD", is to have GitHub Actions run
`kubectl apply`.

**Decision.** GitHub Actions validates and never deploys. Four checks —
`lint` (yamllint), `validate` (kubeconform, strict, with CRD schemas), `render`
(`helm template` on each Helm-backed Application, output fed back through
kubeconform) and `policy` (no plaintext `Secret`, no placeholder text) — gate
`main` as required status checks. Delivery remains Argo CD pulling. No workflow
holds a cluster credential.

**Alternatives rejected.** Actions running `kubectl apply` directly — rejected
on two grounds. The API server is not publicly reachable (ADR-004), so a
GitHub-hosted runner would need a tailnet credential or a self-hosted runner,
which means a long-lived cluster credential in a public repository's secret
store. More fundamentally it would mean two systems writing to the cluster, and
that is the same class of mistake as declaring `replicas` alongside an HPA: two
controllers claiming one field, reconciling against each other. One writer.

Leaving `main` unprotected and letting the checks run post-push was also
rejected: a check that reports after Argo CD has already synced is a post-mortem
tool, not a gate.

**Consequences.** Changes now require a branch and a pull request, which is
friction for a single operator — accepted, because the alternative is that a bad
commit is a live incident. Adding a fifth check means updating branch protection,
since the required contexts are listed explicitly.

The gate is on the repository, not the cluster: anyone with cluster access can
still apply a plaintext Secret directly. Enforcing that at the API server needs
admission control (Kyverno or Gatekeeper) and is deliberately deferred, not
overlooked — the same position as the Cloudflare provider in ADR-005.
