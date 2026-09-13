# ADR-009 — A confidential client with PKCE, not a public client

**Context.** The demo application at `app.szenassy-akos.com` is a server-rendered
Go service that authenticates users against Keycloak using the OAuth 2.0
authorization code flow. OAuth defines two client types. A **public** client —
a single-page app or a mobile app — runs entirely on the user's device and
therefore cannot hold a secret: anything shipped to the browser is readable by
the user and by anyone else who fetches the bundle. A **confidential** client
runs on a server the operator controls and can hold one. PKCE (Proof Key for Code
Exchange) is a separate mechanism that binds an authorization code to the client
instance that requested it.

**Decision.** The client is confidential — `publicClient: false`, authenticating
to the token endpoint with HTTP Basic — **and** it uses PKCE, with
`pkce.code.challenge.method: S256` enforced on the Keycloak side so a client that
omits the challenge is rejected rather than quietly downgraded. The direct access
grant (the password grant) is disabled, so the application is never in a position
to see a user's password even if someone asked it to.

**Alternatives rejected.** A public client with PKCE alone, which would have been
simpler and would have removed the secret-in-the-realm problem that ADR-008
describes at length. Rejected because it would be a weaker configuration chosen
to dodge an implementation inconvenience: this application genuinely can keep a
secret, and declining to use a control that is available is not a trade, it is a
concession. PKCE without client authentication was also rejected for the same
reason — OAuth 2.1 requires PKCE of everyone, so treating it as a substitute for
client authentication rather than an addition to it misreads what it is for.

**Consequences.** The two mechanisms defend against different attackers, and
being able to say which is which is the point of choosing both.

Client authentication proves *who is redeeming the code*: without the secret, an
attacker holding a stolen authorization code cannot present themselves as this
application at the token endpoint. PKCE proves *that the redeemer is the same
party that started the flow*: the verifier is generated per login and never
leaves the server, and only its SHA-256 hash travels in the front-channel
redirect, so intercepting the redirect yields nothing redeemable. A public client
has only the second defence, which is exactly why PKCE became mandatory there
first.

The cost is the secret itself — one more credential with a lifecycle, which has
to reach both Keycloak and the application without being committed. It is sealed
once and read by both, so there is a single source and the two sides cannot
drift. Rotating it means re-sealing and re-importing the realm, which given
ADR-008's one-shot import is more friction than it should be.
