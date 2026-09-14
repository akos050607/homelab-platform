# ADR-010 — Adoption is not ownership: what GitOps does not see

**Context.** `homelab-gitops` is reconciled by Argo CD with `prune: true` and
`selfHeal: true`, and its README asserts that nothing in the cluster is applied
by hand. On 2026-09-14 a new Ingress for `app.szenassy-akos.com` was rejected by
the ingress-nginx admission webhook: the host was already claimed by
`default/test-ingress`. That object, together with `Service/test-app` and
`Service/demo-service`, had been created with `kubectl apply` on 6–7 August
during A3 and never committed. `apps/test-app.yaml` contains only a Deployment.

Argo CD had adopted that Deployment and reported the repository `Synced` and
`Healthy` continuously for 38 days. It was not wrong. `prune` removes resources
that Argo CD tracks and no longer finds in git; it has nothing to say about
resources it never tracked. **Adoption of one object does not extend ownership to
its neighbours**, and there is no signal anywhere in the system when an
untracked object exists — no error, no drift, no warning. The declared state
simply stops describing the cluster, quietly.

**Decision.** The three orphans were deleted with `kubectl`, which is the honest
ending: git cannot express the deletion of something it never owned, so the
hand-made mess had to be cleaned up by hand. The README claim is corrected to
state the boundary rather than assert an absolute, and the boundary is this —
**Argo CD guarantees that what is in git is in the cluster. It does not
guarantee the converse.**

**Alternatives rejected.** Moving `test-app`'s Service and Ingress into git,
rejected because it would have preserved a hello-world nginx on a public
hostname to avoid admitting a mistake. Pointing the new application at a
different hostname, rejected for the same reason: it routes around the problem
and leaves the untracked objects in place, still invisible, still holding a
production certificate.

**Consequences.** The gap is structural, not an oversight to be fixed by being
more careful, and three things narrow it — none of which this cluster has today.

An Argo CD **AppProject with `orphanedResources` monitoring** would surface
untracked objects in the namespaces it governs. That is the cheapest of the
three and the obvious next step. (It is also blocked behind a separate defect:
the `applicationset-controller` has been crash-looping since installation
because its CRD never applied — noted here because the two share a root, which
is that `kubectl apply` of a large upstream manifest fails partially and
silently.)

**Admission control** — Kyverno or Gatekeeper — would refuse an object that
carries no Argo CD tracking label, which stops the class at the API server
rather than reporting it afterwards. ADR-007 deferred this deliberately and
named it as the obvious next layer; this incident is that ADR's closing argument
arriving in practice.

Third and most simply: **the cluster should be periodically rebuilt from zero**,
because a rebuilt cluster contains exactly what git describes and nothing else.
A hand-applied object survives only as long as the cluster it was applied to.

The most uncomfortable part is worth stating plainly. Four systems were watching
this cluster — CI, Argo CD, Prometheus, and cert-manager — and none of them
raised anything. CI only reads the repository. Argo CD only reconciles what it
tracks. Prometheus was scraping the orphan happily, because it was running fine.
cert-manager renewed its certificate on schedule, because that is its job. What
found it was an admission webhook belonging to a component installed for an
entirely unrelated reason, and only because something else happened to want the
same hostname. **A monitoring stack that reports green is evidence that the
things being watched are healthy, not evidence that everything present is
supposed to be there.**
