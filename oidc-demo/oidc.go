// OpenID Connect: discovery, JWKS retrieval, and ID token verification.
//
// Written against the standard library on purpose. github.com/coreos/go-oidc
// would reduce this file to about twenty lines, and at work that is the right
// call — but the entire point of this service is to be able to say what
// verifying a token actually involves, and a library call cannot say it.
package main

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// discovery is the subset of the OIDC discovery document this app uses. The
// endpoints are never hardcoded: everything below is read from
// {issuer}/.well-known/openid-configuration at startup. That single document is
// the practical difference from SAML, where the same information arrives as a
// metadata file both sides have to exchange up front.
type discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	EndSessionEndpoint    string `json:"end_session_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
}

// jwk is one key from the JWKS. Only RSA signing keys are handled; Keycloak's
// default signature algorithm is RS256.
type jwk struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwks struct {
	Keys []jwk `json:"keys"`
}

// keyCache holds the provider's public keys. They rotate, so an unknown `kid`
// triggers exactly one refetch rather than an error — and a refetch is rate
// limited, because otherwise a token with a garbage kid is a free way to make
// this service hammer the identity provider.
type keyCache struct {
	mu          sync.RWMutex
	uri         string
	keys        map[string]*rsa.PublicKey
	lastFetched time.Time
	client      *http.Client
}

func newKeyCache(uri string, client *http.Client) *keyCache {
	return &keyCache{uri: uri, keys: map[string]*rsa.PublicKey{}, client: client}
}

func (kc *keyCache) refresh() error {
	resp, err := kc.client.Get(kc.uri)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch jwks: status %d", resp.StatusCode)
	}

	var set jwks
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return fmt.Errorf("decode jwks: %w", err)
	}

	parsed := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		// `use: enc` keys are for encryption, not signatures. Accepting one
		// here would mean verifying a signature against a key never intended
		// to make one.
		if k.Kty != "RSA" || (k.Use != "" && k.Use != "sig") {
			continue
		}
		pub, err := k.rsaPublicKey()
		if err != nil {
			continue
		}
		parsed[k.Kid] = pub
	}
	if len(parsed) == 0 {
		return errors.New("jwks contained no usable RSA signing keys")
	}

	kc.mu.Lock()
	kc.keys, kc.lastFetched = parsed, time.Now()
	kc.mu.Unlock()
	return nil
}

// rsaPublicKey rebuilds the public key from its modulus and exponent. A JWKS
// does not ship a PEM — it ships the two numbers, base64url encoded, and the
// key is reconstructed from them.
func (k jwk) rsaPublicKey() (*rsa.PublicKey, error) {
	nb, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("decode modulus: %w", err)
	}
	eb, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("decode exponent: %w", err)
	}
	e := new(big.Int).SetBytes(eb)
	if !e.IsInt64() || e.Int64() > 1<<31-1 {
		return nil, errors.New("implausible public exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: int(e.Int64())}, nil
}

func (kc *keyCache) keyFor(kid string) (*rsa.PublicKey, error) {
	kc.mu.RLock()
	key, ok := kc.keys[kid]
	age := time.Since(kc.lastFetched)
	kc.mu.RUnlock()
	if ok {
		return key, nil
	}
	// Unknown kid: the provider may have rotated. Refetch at most once a minute
	// so an attacker cannot turn forged tokens into outbound request volume.
	if age < time.Minute {
		return nil, fmt.Errorf("no key for kid %q", kid)
	}
	if err := kc.refresh(); err != nil {
		return nil, err
	}
	kc.mu.RLock()
	defer kc.mu.RUnlock()
	if key, ok := kc.keys[kid]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("no key for kid %q after refresh", kid)
}

// claims is the decoded ID token payload.
type claims struct {
	Issuer    string   `json:"iss"`
	Subject   string   `json:"sub"`
	Audience  audience `json:"aud"`
	Expiry    int64    `json:"exp"`
	IssuedAt  int64    `json:"iat"`
	NotBefore int64    `json:"nbf"`
	AuthTime  int64    `json:"auth_time"`
	Nonce     string   `json:"nonce"`
	AZP       string   `json:"azp"`
	Username  string   `json:"preferred_username"`
	Email     string   `json:"email"`
	Name      string   `json:"name"`
	AMR       []string `json:"amr"`
	RealmAcc  struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`

	// Raw payload, so /me can show every claim rather than only the modelled ones.
	raw map[string]any
}

// audience exists because `aud` is "a string or an array of strings" in the
// spec, and a plain []string field fails to unmarshal the single-string form.
type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*a = audience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

func (a audience) contains(s string) bool {
	for _, v := range a {
		if v == s {
			return true
		}
	}
	return false
}

// leeway absorbs clock skew between this pod and Keycloak. Token validation
// compares exp/iat/nbf against the *verifier's* clock, so a drifting node
// produces tokens that are already expired or not yet valid.
const leeway = 60 * time.Second

// verifyIDToken checks the signature and then the claims, in that order.
//
// Order matters: every claim is attacker-controlled until the signature says
// otherwise, so nothing in the payload may be trusted to decide how to verify.
func verifyIDToken(raw string, kc *keyCache, issuer, clientID, nonce string) (*claims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errors.New("token is not three dot-separated segments")
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode header: %w", err)
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
	}

	// Only RS256. This is the algorithm-confusion defence and it is not
	// theoretical: "alg": "none" asks the verifier to skip the check entirely,
	// and "alg": "HS256" asks it to verify a symmetric MAC using the public key
	// as the shared key — a key the attacker also has, because it is public.
	// The fix is to decide the acceptable algorithm here rather than let the
	// token nominate one.
	if header.Alg != "RS256" {
		return nil, fmt.Errorf("unexpected signing algorithm %q, want RS256", header.Alg)
	}

	pub, err := kc.keyFor(header.Kid)
	if err != nil {
		return nil, err
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}
	// The signature covers the first two segments and the dot between them,
	// exactly as they arrived — not a re-serialisation of the parsed JSON.
	signed := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, signed[:], sig); err != nil {
		return nil, fmt.Errorf("signature verification failed: %w", err)
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	var c claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, fmt.Errorf("parse claims: %w", err)
	}
	_ = json.Unmarshal(payload, &c.raw)

	// Signature proves the token was minted by the holder of that key. It says
	// nothing about whether the token was minted for THIS application, by the
	// provider we trust, or recently. That is what the claims are for.
	now := time.Now()

	if c.Issuer != issuer {
		return nil, fmt.Errorf("issuer %q does not match %q", c.Issuer, issuer)
	}
	// Without the audience check, a token issued to any other client of the
	// same realm would be accepted here — a valid signature from a legitimate
	// provider, replayed into an application it was never meant for.
	if !c.Audience.contains(clientID) {
		return nil, fmt.Errorf("audience %v does not contain %q", c.Audience, clientID)
	}
	if c.AZP != "" && c.AZP != clientID {
		return nil, fmt.Errorf("authorized party %q is not %q", c.AZP, clientID)
	}
	if c.Expiry == 0 || now.After(time.Unix(c.Expiry, 0).Add(leeway)) {
		return nil, errors.New("token has expired")
	}
	if c.NotBefore != 0 && now.Add(leeway).Before(time.Unix(c.NotBefore, 0)) {
		return nil, errors.New("token is not valid yet")
	}
	// The nonce binds this token to the authorization request this browser
	// started, which is what stops a token obtained elsewhere being injected
	// into someone else's session.
	if nonce != "" && c.Nonce != nonce {
		return nil, errors.New("nonce does not match the authorization request")
	}

	return &c, nil
}
