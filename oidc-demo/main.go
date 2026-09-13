// A minimal OpenID Connect relying party.
//
// It exists to demonstrate the authorization code flow with PKCE against
// Keycloak, end to end, in code small enough to read in one sitting: the
// browser is redirected to the identity provider, comes back with a code, and
// the code is exchanged for tokens on a back channel the browser never sees.
//
// The interesting property is that this service never sees a password. It holds
// a client secret and a redirect URI, and everything it knows about the user
// arrives inside a signed token it verifies itself — see oidc.go.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

var version = "dev"

type app struct {
	disc     discovery
	keys     *keyCache
	clientID string
	secret   string
	redirect string
	baseURL  string
	http     *http.Client
}

// txState is what has to survive the round trip to Keycloak and back. It rides
// in an httpOnly cookie rather than server memory so that this service stays
// stateless and a restart mid-login is not a broken login.
type txState struct {
	State    string `json:"state"`
	Verifier string `json:"verifier"`
	Nonce    string `json:"nonce"`
}

const (
	txCookie      = "oidc_tx"
	sessionCookie = "oidc_session"
)

func main() {
	a := &app{
		clientID: mustEnv("OIDC_CLIENT_ID"),
		secret:   os.Getenv("OIDC_CLIENT_SECRET"),
		redirect: mustEnv("OIDC_REDIRECT_URI"),
		baseURL:  strings.TrimSuffix(mustEnv("APP_BASE_URL"), "/"),
		http:     &http.Client{Timeout: 10 * time.Second},
	}
	issuer := strings.TrimSuffix(mustEnv("OIDC_ISSUER"), "/")

	// Discovery at startup, with retries: on a cold cluster this pod and
	// Keycloak come up together, and failing permanently because the provider
	// was four seconds behind would be a self-inflicted outage.
	if err := a.discover(issuer); err != nil {
		log.Fatalf("discovery failed: %v", err)
	}
	log.Printf("discovered issuer=%s", a.disc.Issuer)

	a.keys = newKeyCache(a.disc.JWKSURI, a.http)
	if err := a.keys.refresh(); err != nil {
		log.Fatalf("initial JWKS fetch failed: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, version)
	})
	mux.HandleFunc("/", a.handleIndex)
	mux.HandleFunc("/login", a.handleLogin)
	mux.HandleFunc("/callback", a.handleCallback)
	mux.HandleFunc("/me", a.handleMe)
	mux.HandleFunc("/logout", a.handleLogout)
	mux.HandleFunc("/saml/acs", a.handleSAMLACS)

	port := getenv("PORT", "8080")
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	idle := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
		<-sig
		log.Println("shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("graceful shutdown failed: %v", err)
		}
		close(idle)
	}()

	log.Printf("listening on :%s (version=%s, client=%s)", port, version, a.clientID)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server error: %v", err)
	}
	<-idle
}

func (a *app) discover(issuer string) error {
	u := issuer + "/.well-known/openid-configuration"
	var lastErr error
	for attempt := 1; attempt <= 10; attempt++ {
		resp, err := a.http.Get(u)
		if err == nil {
			func() {
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					lastErr = fmt.Errorf("status %d", resp.StatusCode)
					return
				}
				lastErr = json.NewDecoder(resp.Body).Decode(&a.disc)
			}()
			if lastErr == nil {
				// The document says who it belongs to. If that disagrees with
				// the issuer we were configured with, we are talking to the
				// wrong provider and should not carry on.
				if a.disc.Issuer != issuer {
					return fmt.Errorf("discovery issuer %q != configured %q", a.disc.Issuer, issuer)
				}
				return nil
			}
		} else {
			lastErr = err
		}
		log.Printf("discovery attempt %d/10 failed: %v", attempt, lastErr)
		time.Sleep(time.Duration(attempt) * 2 * time.Second)
	}
	return lastErr
}

// ---------------------------------------------------------------------------
// The flow
// ---------------------------------------------------------------------------

func (a *app) handleLogin(w http.ResponseWriter, r *http.Request) {
	// PKCE. The verifier is a secret this client keeps; only its SHA-256 hash
	// travels in the front-channel redirect. An attacker who intercepts the
	// authorization code cannot redeem it, because the token request must also
	// present the verifier that hashes to the challenge sent earlier.
	verifier := randomString(64)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	tx := txState{
		// state is CSRF protection for the callback: it ties the response
		// coming back to a request this browser actually started.
		State: randomString(32),
		// nonce ties the resulting ID token to this same request.
		Nonce:    randomString(32),
		Verifier: verifier,
	}
	blob, _ := json.Marshal(tx)
	http.SetCookie(w, &http.Cookie{
		Name:     txCookie,
		Value:    base64.RawURLEncoding.EncodeToString(blob),
		Path:     "/",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   true,
		// Lax rather than Strict: the callback arrives as a cross-site
		// top-level navigation from Keycloak, and Strict would withhold the
		// cookie exactly then.
		SameSite: http.SameSiteLaxMode,
	})

	q := url.Values{
		"client_id":             {a.clientID},
		"redirect_uri":          {a.redirect},
		"response_type":         {"code"},
		"scope":                 {"openid profile email"},
		"state":                 {tx.State},
		"nonce":                 {tx.Nonce},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	http.Redirect(w, r, a.disc.AuthorizationEndpoint+"?"+q.Encode(), http.StatusFound)
}

func (a *app) handleCallback(w http.ResponseWriter, r *http.Request) {
	if e := r.URL.Query().Get("error"); e != "" {
		a.fail(w, http.StatusBadRequest, "authorization failed: %s: %s", e, r.URL.Query().Get("error_description"))
		return
	}

	c, err := r.Cookie(txCookie)
	if err != nil {
		a.fail(w, http.StatusBadRequest, "no login in progress (transaction cookie missing)")
		return
	}
	blob, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		a.fail(w, http.StatusBadRequest, "malformed transaction cookie")
		return
	}
	var tx txState
	if err := json.Unmarshal(blob, &tx); err != nil {
		a.fail(w, http.StatusBadRequest, "malformed transaction cookie")
		return
	}
	// Compared before anything else is done with the response.
	if got := r.URL.Query().Get("state"); got != tx.State {
		a.fail(w, http.StatusBadRequest, "state mismatch: this response does not belong to a login this browser started")
		return
	}
	clearCookie(w, txCookie)

	code := r.URL.Query().Get("code")
	if code == "" {
		a.fail(w, http.StatusBadRequest, "no authorization code in callback")
		return
	}

	// The back channel. The code arrived through the browser; it is redeemed
	// server to server, so the tokens themselves never touch the user agent.
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {a.redirect},
		"client_id":     {a.clientID},
		"code_verifier": {tx.Verifier},
	}
	req, _ := http.NewRequest(http.MethodPost, a.disc.TokenEndpoint, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Confidential client: it authenticates itself with HTTP Basic rather than
	// putting the secret in the body. A public client (an SPA, a mobile app)
	// could not do this at all, which is why PKCE is mandatory there and
	// belt-and-braces here.
	req.SetBasicAuth(url.QueryEscape(a.clientID), url.QueryEscape(a.secret))

	resp, err := a.http.Do(req)
	if err != nil {
		a.fail(w, http.StatusBadGateway, "token request failed: %v", err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		a.fail(w, http.StatusBadGateway, "token endpoint returned %d: %s", resp.StatusCode, body)
		return
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		a.fail(w, http.StatusBadGateway, "malformed token response: %v", err)
		return
	}
	if tok.IDToken == "" {
		a.fail(w, http.StatusBadGateway, "token response contained no id_token")
		return
	}

	// Verified here, and verified again on every /me request. A token is not
	// trusted because it arrived from a source we trust; it is trusted because
	// it verifies.
	if _, err := verifyIDToken(tok.IDToken, a.keys, a.disc.Issuer, a.clientID, tx.Nonce); err != nil {
		a.fail(w, http.StatusUnauthorized, "ID token rejected: %v", err)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:  sessionCookie,
		Value: tok.IDToken,
		Path:  "/",
		// httpOnly is the point: script on this page cannot read it. A token in
		// localStorage is readable by any XSS on the origin, which is why
		// "just put the JWT in localStorage" is a bad default.
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   tok.ExpiresIn,
	})
	http.Redirect(w, r, "/me", http.StatusFound)
}

func (a *app) handleLogout(w http.ResponseWriter, r *http.Request) {
	var hint string
	if c, err := r.Cookie(sessionCookie); err == nil {
		hint = c.Value
	}
	clearCookie(w, sessionCookie)
	clearCookie(w, txCookie)

	// Clearing the local cookie ends the session with THIS app only. The
	// session at the identity provider is separate, and ending it is what makes
	// the next login prompt again rather than sail straight through.
	if a.disc.EndSessionEndpoint == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	q := url.Values{"post_logout_redirect_uri": {a.baseURL + "/"}, "client_id": {a.clientID}}
	if hint != "" {
		q.Set("id_token_hint", hint)
	}
	http.Redirect(w, r, a.disc.EndSessionEndpoint+"?"+q.Encode(), http.StatusFound)
}

// ---------------------------------------------------------------------------
// SAML, for comparison only
// ---------------------------------------------------------------------------

// handleSAMLACS is an assertion consumer service in the loosest possible sense:
// it decodes the POSTed assertion and shows it, and deliberately does not
// validate the XML signature.
//
// That is the honest scope. Verifying an XML signature properly means
// canonicalising the document first, and XML canonicalisation is exactly why
// SAML implementations are notoriously easy to get wrong. This endpoint exists
// so the difference between the two protocols can be seen rather than recited:
// an auto-submitted browser POST carrying signed XML, against a JSON document
// fetched from a well-known URL.
func (a *app) handleSAMLACS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.fail(w, http.StatusMethodNotAllowed, "the SAML ACS endpoint is reached by POST, not GET — that is the binding")
		return
	}
	if err := r.ParseForm(); err != nil {
		a.fail(w, http.StatusBadRequest, "cannot parse form: %v", err)
		return
	}
	raw := r.PostFormValue("SAMLResponse")
	if raw == "" {
		a.fail(w, http.StatusBadRequest, "no SAMLResponse field in the POST body")
		return
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		a.fail(w, http.StatusBadRequest, "SAMLResponse is not valid base64: %v", err)
		return
	}
	pretty, err := indentXML(decoded)
	if err != nil {
		pretty = decoded
	}
	render(w, samlTmpl, map[string]any{"XML": string(pretty), "Bytes": len(decoded), "Version": version})
}

func indentXML(in []byte) ([]byte, error) {
	var out strings.Builder
	dec := xml.NewDecoder(strings.NewReader(string(in)))
	enc := xml.NewEncoder(&out)
	enc.Indent("", "  ")
	for {
		t, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if err := enc.EncodeToken(t); err != nil {
			return nil, err
		}
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return []byte(out.String()), nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true})
}

func (a *app) fail(w http.ResponseWriter, code int, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Printf("error: %s", msg)
	w.WriteHeader(code)
	render(w, errorTmpl, map[string]any{"Message": msg, "Code": code, "Version": version})
}

func render(w http.ResponseWriter, t *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.Execute(w, data); err != nil {
		log.Printf("template error: %v", err)
	}
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required environment variable %s is not set", key)
	}
	return v
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
