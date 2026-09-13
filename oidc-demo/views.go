// The pages. Server-rendered with html/template, which escapes by default —
// every value below arrives from a token and is therefore attacker-influenced
// until proven otherwise.
package main

import (
	"encoding/base64"
	"encoding/json"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"
)

func (a *app) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		a.fail(w, http.StatusNotFound, "no such page: %s", r.URL.Path)
		return
	}
	_, err := r.Cookie(sessionCookie)
	render(w, indexTmpl, map[string]any{
		"LoggedIn": err == nil,
		"Issuer":   a.disc.Issuer,
		"ClientID": a.clientID,
		"Version":  version,
	})
}

type claimRow struct {
	Key, Value, Note string
}

func (a *app) handleMe(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	// Re-verified on every request, not trusted because it was verified once at
	// login. The cookie is httpOnly but it still came from the client, and an
	// expired token must stop working the moment it expires.
	//
	// Nonce is empty here deliberately: the nonce binds a token to one
	// authorization request and was checked at /callback. There is no
	// authorization request in flight now, so there is nothing to bind to.
	cl, err := verifyIDToken(c.Value, a.keys, a.disc.Issuer, a.clientID, "")
	if err != nil {
		clearCookie(w, sessionCookie)
		a.fail(w, http.StatusUnauthorized, "session token no longer valid: %v", err)
		return
	}

	notes := map[string]string{
		"iss":                "who issued it — must be the provider we trust",
		"aud":                "who it is FOR — must be this client, or a valid token for another app would be accepted here",
		"azp":                "authorized party: the client that requested it",
		"exp":                "expiry, checked against this pod's clock",
		"iat":                "issued at",
		"nbf":                "not valid before",
		"sub":                "the stable user identifier — this, not the username, is the thing to key on",
		"nonce":              "binds the token to the login request that started it",
		"preferred_username": "a display name, and it can change — never a primary key",
		"realm_access":       "the roles, carried in the token itself",
		"amr":                "how the user actually authenticated",
		"auth_time":          "when the user authenticated, which is not when the token was issued",
		"sid":                "the provider's session id",
		"typ":                "token type",
		"acr":                "authentication context class",
	}

	rows := make([]claimRow, 0, len(cl.raw))
	for k, v := range cl.raw {
		var s string
		switch t := v.(type) {
		case string:
			s = t
		case float64:
			s = trimFloat(t)
			// Unix timestamps are unreadable as integers; show what they mean.
			if k == "exp" || k == "iat" || k == "nbf" || k == "auth_time" {
				s = s + "  (" + time.Unix(int64(t), 0).UTC().Format("2006-01-02 15:04:05 UTC") + ")"
			}
		default:
			b, _ := json.Marshal(v)
			s = string(b)
		}
		rows = append(rows, claimRow{Key: k, Value: s, Note: notes[k]})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })

	parts := strings.Split(c.Value, ".")
	headerJSON := ""
	if hb, err := base64.RawURLEncoding.DecodeString(parts[0]); err == nil {
		headerJSON = string(hb)
	}

	render(w, meTmpl, map[string]any{
		"Rows":         rows,
		"Header":       headerJSON,
		"Username":     cl.Username,
		"Subject":      cl.Subject,
		"Roles":        cl.RealmAcc.Roles,
		"AMR":          cl.AMR,
		"Passwordless": containsAny(cl.AMR, "webauthn", "passkey"),
		"Expiry":       time.Unix(cl.Expiry, 0).UTC().Format("15:04:05 UTC"),
		"JWKSURI":      a.disc.JWKSURI,
		"Version":      version,
	})
}

func containsAny(hay []string, needles ...string) bool {
	for _, h := range hay {
		for _, n := range needles {
			if strings.Contains(strings.ToLower(h), n) {
				return true
			}
		}
	}
	return false
}

func trimFloat(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

const styles = `
:root { color-scheme: light dark; --fg:#111; --muted:#666; --bg:#fff; --line:#e3e3e3; --accent:#1f6feb; --ok:#0a7c42; }
@media (prefers-color-scheme: dark) {
  :root { --fg:#e8e8e8; --muted:#9b9b9b; --bg:#121212; --line:#2c2c2c; --accent:#589dff; --ok:#3fb950; }
}
* { box-sizing: border-box; }
body { margin:0; padding:2rem 1rem; background:var(--bg); color:var(--fg);
       font: 15px/1.6 ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif; }
main { max-width: 60rem; margin: 0 auto; }
h1 { font-size:1.5rem; margin:0 0 .25rem; letter-spacing:-.01em; }
h2 { font-size:1rem; margin:2rem 0 .5rem; text-transform:uppercase; letter-spacing:.06em; color:var(--muted); }
.sub { color:var(--muted); margin:0 0 2rem; }
code, pre, .mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size:13px; }
pre { background:rgba(128,128,128,.09); border:1px solid var(--line); border-radius:6px;
      padding:.85rem; overflow-x:auto; }
table { border-collapse:collapse; width:100%; }
td, th { border-bottom:1px solid var(--line); padding:.5rem .6rem; text-align:left; vertical-align:top; }
th { color:var(--muted); font-weight:600; font-size:.8rem; text-transform:uppercase; letter-spacing:.05em; }
td.k { font-family:ui-monospace,monospace; white-space:nowrap; font-weight:600; }
td.v { font-family:ui-monospace,monospace; word-break:break-word; }
td.n { color:var(--muted); font-size:.85rem; }
a.btn { display:inline-block; background:var(--accent); color:#fff; text-decoration:none;
        padding:.6rem 1.1rem; border-radius:6px; font-weight:600; }
a.btn.ghost { background:transparent; color:var(--accent); border:1px solid var(--accent); }
.badge { display:inline-block; padding:.15rem .5rem; border-radius:99px; font-size:.8rem;
         font-weight:600; background:rgba(10,124,66,.14); color:var(--ok); }
.row { display:flex; gap:.75rem; flex-wrap:wrap; align-items:center; }
footer { margin-top:3rem; color:var(--muted); font-size:.82rem; border-top:1px solid var(--line); padding-top:1rem; }
@media (max-width: 600px) { td.n { display:none; } body { padding:1.25rem 1rem; } }
`

var base = `{{define "head"}}<style>` + styles + `</style>{{end}}
{{define "foot"}}<footer>oidc-demo {{.Version}} · verified in-process against the provider's JWKS · no password ever reaches this service</footer>{{end}}`

var indexTmpl = template.Must(template.New("index").Parse(base + `
{{template "head" .}}
<main>
  <h1>OIDC demo</h1>
  <p class="sub">A relying party for <code>{{.Issuer}}</code>, client <code>{{.ClientID}}</code>.</p>
  {{if .LoggedIn}}
    <div class="row">
      <a class="btn" href="/me">View my token claims</a>
      <a class="btn ghost" href="/logout">Log out</a>
    </div>
  {{else}}
    <div class="row"><a class="btn" href="/login">Log in</a></div>
    <h2>What happens when you click that</h2>
    <ol>
      <li>This service generates a <code>state</code>, a <code>nonce</code> and a PKCE
          <code>code_verifier</code>, and redirects you to the provider carrying only the
          SHA-256 <em>hash</em> of the verifier.</li>
      <li>You authenticate there. This service never sees a password or a passkey.</li>
      <li>The provider redirects you back with a one-time <code>code</code>.</li>
      <li>This service exchanges that code for tokens over a back channel your browser
          never touches, proving it holds both the client secret and the original verifier.</li>
      <li>The ID token's signature is checked against the provider's published keys
          before a single claim inside it is believed.</li>
    </ol>
  {{end}}
{{template "foot" .}}
</main>`))

var meTmpl = template.Must(template.New("me").Parse(base + `
{{template "head" .}}
<main>
  <h1>Signed in as {{.Username}}</h1>
  <p class="sub">
    {{if .Passwordless}}<span class="badge">passwordless — no password was used</span>{{end}}
    Session valid until {{.Expiry}}.
  </p>

  <h2>Token header</h2>
  <pre>{{.Header}}</pre>
  <p class="sub"><code>kid</code> selects which key from
     <code>{{.JWKSURI}}</code> verifies this signature.
     <code>alg</code> is checked against RS256 rather than trusted — a token asking to be
     verified with <code>none</code> is rejected, not obeyed.</p>

  <h2>Claims, after verification</h2>
  <table>
    <tr><th>Claim</th><th>Value</th><th>Why it matters</th></tr>
    {{range .Rows}}<tr><td class="k">{{.Key}}</td><td class="v">{{.Value}}</td><td class="n">{{.Note}}</td></tr>{{end}}
  </table>

  <div class="row" style="margin-top:2rem">
    <a class="btn ghost" href="/logout">Log out</a>
  </div>
{{template "foot" .}}
</main>`))

var samlTmpl = template.Must(template.New("saml").Parse(base + `
{{template "head" .}}
<main>
  <h1>SAML assertion received</h1>
  <p class="sub">{{.Bytes}} bytes, delivered by an auto-submitted browser POST — not a JSON API call.
     The signature is <strong>not</strong> validated here; this endpoint exists to show the
     shape of the protocol, not to consume it in production.</p>
  <pre>{{.XML}}</pre>
{{template "foot" .}}
</main>`))

var errorTmpl = template.Must(template.New("error").Parse(base + `
{{template "head" .}}
<main>
  <h1>{{.Code}}</h1>
  <pre>{{.Message}}</pre>
  <div class="row"><a class="btn ghost" href="/">Start again</a></div>
{{template "foot" .}}
</main>`))
