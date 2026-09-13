# ADR-008 — Keycloak as a plain Deployment, not the Keycloak Operator

**Context.** The identity stack needed Keycloak on the cluster with its realm
configuration in git, because a realm clicked together in an admin console is not
reproducible and cannot be part of a GitOps story. Keycloak ships an official
operator with two CRDs — `Keycloak` and `KeycloakRealmImport` — and the operator
is the path the upstream documentation recommends. The alternative is the stock
container image driven by environment variables and a `--import-realm` flag.

**Decision.** A plain `Deployment` running `quay.io/keycloak/keycloak:26.7.3`,
with the realm mounted from a ConfigMap. Every setting that makes it work —
`KC_PROXY_HEADERS`, the health port, the database wiring, the security context —
is a line in a manifest in this repository, and the reasoning for each is a
comment beside it. The operator would install a StatefulSet whose shape is
decided upstream.

**Alternatives rejected.** The Keycloak Operator, rejected on explainability
rather than capability, and the cost of that is real and is stated below. The
Bitnami chart, rejected outright: the free Docker Hub images moved to a
`bitnamilegacy` namespace in 2025 and most surviving tutorials reference tags
that no longer resolve.

**Consequences.** Two capabilities were given up, both of which the operator
would have provided.

The first is **variable substitution**. `KeycloakRealmImport` has a
`spec.placeholders` stanza that injects values from Kubernetes Secrets into the
realm JSON. Plain Keycloak has nothing equivalent — a `$` in a realm file is
ignored, and `kc.sh import` rejects one outright with `Character '$' not
allowed` (keycloak/keycloak#12069, #20199). Getting the confidential client's
secret into the realm without committing it therefore needed a mechanism of its
own: an initContainer substitutes the sealed values into the JSON and writes the
result to an `emptyDir` before Keycloak ever opens the file. It fails the pod if
any placeholder survives, because a realm imported with the literal text
`__DEMO_USER_PASSWORD__` as a password is a failure that should be loud.

The second is **reconciliation**, and it is the more serious one.
`--import-realm` imports a realm only when that realm is *absent*; the startup
log says so in as many words — `Full model import requested. Strategy:
IGNORE_EXISTING`. Editing the committed realm does not change the running one.
Argo CD will report the ConfigMap as Synced while the live realm differs from it,
which is the same silent-divergence class as two controllers owning one field: no
error, no alert, just a declared state that quietly stops corresponding to
reality. The realm is re-imported by deleting it or running `kc.sh import` by
hand, and that is a manual step in a repository whose entire premise is that
there are none.

Also accepted: the image is started with `start` rather than `start
--optimized`, because `--optimized` skips the build step instead of performing
it and fails on an image that has not had `kc.sh build --db=postgres` run against
it. Every cold start therefore pays a Quarkus augmentation step, and
`readOnlyRootFilesystem` cannot be enabled because that step writes back into the
image filesystem. Building a purpose-built Keycloak image in the GHCR pipeline
would fix both at once and is the obvious next move — the same position as
Kyverno in ADR-007: deferred deliberately, not overlooked.
