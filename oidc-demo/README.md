# oidc-demo

An OpenID Connect relying party, in about 700 lines of Go with no dependencies
outside the standard library. It exists to demonstrate the authorization code
flow with PKCE against the Keycloak at `auth.szenassy-akos.com`, and to make
every step of that flow something that can be pointed at rather than described.

Live at **https://app.szenassy-akos.com**.

## The flow, as this code performs it

| Step | Where | What happens |
|---|---|---|
| 1 | `GET /login` | Generate `state`, `nonce` and a PKCE `code_verifier`. Redirect to the provider carrying only the SHA-256 **hash** of the verifier. |
| 2 | Keycloak | The user authenticates. **This service never sees a password or a passkey.** |
| 3 | `GET /callback` | Compare the returned `state` against the one in the transaction cookie, before anything else is done with the response. |
| 4 | back channel | `POST` the code plus the `code_verifier` to the token endpoint, authenticating with the client secret. The browser never sees this request or its response. |
| 5 | `oidc.go` | Verify the ID token's RS256 signature against the provider's JWKS, then check `iss`, `aud`, `azp`, `exp`, `nbf` and `nonce`. |
| 6 | `GET /me` | Render every claim, with a note on what each one is actually for. |

## Why the verification is hand-written

`github.com/coreos/go-oidc` would reduce `oidc.go` to roughly twenty lines, and
in production that is the right call — a well-reviewed library beats a hand-roll
every time. It is written out here because the whole purpose of this service is
to be able to say what verifying a token involves, and a library call cannot say
it. The order in particular matters:

**Signature first, claims second.** Every value in the payload is
attacker-controlled until the signature says otherwise, so nothing inside the
token may be trusted to decide how the token is verified.

That is not a hypothetical. The `alg` header is a field the *token* supplies,
and two values in it are attacks:

- `"alg": "none"` asks the verifier to skip the check entirely.
- `"alg": "HS256"` asks it to verify a symmetric MAC using the RSA public key as
  the shared key — a key the attacker also has, because it is published.

The defence is to decide the acceptable algorithm in the verifier rather than
let the token nominate one. `TestAlgorithmConfusionIsRejected` covers both, plus
`RS512` and an empty `alg`.

## What the tests assert

Twelve tests, and the interesting ones are the rejections — each mints a
genuinely well-formed JWT that must nevertheless be refused:

- signed by a key we do not trust
- `alg` set to `none`, `HS256`, `RS512`, or empty
- a **valid** token issued to a different client of the same realm (`aud` check —
  without it, any other app's token would be accepted here)
- a valid token from a different issuer
- an expired token
- a token bound to a different authorization request (`nonce`)
- an unknown `kid`, which must fail fast rather than turn forged tokens into
  outbound request volume against the provider

Plus `aud` parsing, which is the one that bites in practice: the spec says
"a string **or** an array of strings", and the array form is what a naive
`[]string` field silently fails on.

## Session handling

The ID token is stored in an `httpOnly`, `Secure`, `SameSite=Lax` cookie and
**re-verified on every `/me` request** — not trusted because it was verified once
at login. `httpOnly` is the substantive choice: a token in `localStorage` is
readable by any XSS on the origin, which is why "put the JWT in localStorage" is
a bad default. `Lax` rather than `Strict` because the callback arrives as a
cross-site top-level navigation from Keycloak, and `Strict` would withhold the
cookie exactly then.

## `/saml/acs`

A SAML assertion consumer service in the loosest sense: it base64-decodes the
POSTed `SAMLResponse` and pretty-prints the XML. It deliberately **does not**
validate the signature, and the scope is honest rather than hidden — verifying an
XML signature properly means canonicalising the document first, and XML
canonicalisation is precisely why SAML implementations are notoriously easy to
get wrong.

It exists so the difference between the two protocols can be *seen*: an
auto-submitted browser POST carrying signed XML, against a JSON document fetched
from one well-known URL.

### The indentation bug that is worth keeping as a comment

The first version pretty-printed by decoding with `encoding/xml` and re-encoding
it. The output was valid XML and completely wrong: Go's encoder does not
preserve namespace prefixes, so `<samlp:Response>` came back as `<Response
xmlns="...">` carrying an invented `_xmlns:samlp="xmlns"` attribute.

That is the entire reason XML signatures are difficult. A SAML signature covers
the **exact bytes** of the assertion. Any transformation producing a
semantically equivalent document — reordering attributes, rewriting a namespace
prefix, changing whitespace — produces a different byte sequence and breaks the
signature. Canonicalisation (c14n) exists to define one normal form so both
sides hash the same bytes, and re-serialising with a general-purpose XML library
is exactly the mistake that breaks it.

`indentXML` now only ever inserts whitespace between `>` and `<`; every byte of
every tag passes through untouched.
`TestIndentXMLPreservesNamespacePrefixesAndTags` asserts that the document is
byte-identical once whitespace is stripped, which the `encoding/xml` version
fails.

## Configuration

| Variable | Purpose |
|---|---|
| `OIDC_ISSUER` | Realm URL. Every endpoint is discovered from it; none are hardcoded. |
| `OIDC_CLIENT_ID` | `oidc-demo` |
| `OIDC_CLIENT_SECRET` | From the SealedSecret in `homelab-gitops/identity/02`. |
| `OIDC_REDIRECT_URI` | Must match the realm registration **exactly** — a loose match is an open redirect, which is a token-theft primitive. |
| `APP_BASE_URL` | Used for `post_logout_redirect_uri`. |
| `PORT` | Default `8080`. |

Discovery retries ten times with a rising backoff: on a cold cluster this pod and
Keycloak start together, and failing permanently because the provider was four
seconds behind would be a self-inflicted outage.

## Image

Multi-stage, ending at `gcr.io/distroless/static-debian12:nonroot` — no shell, no
package manager, nothing to pivot with. `go vet` and `go test` run *inside* the
build stage, so an image that exists is an image whose tests passed, which a
separate CI job cannot promise.
