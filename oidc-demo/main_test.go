package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"
)

// mint builds a signed JWT so the verifier can be tested against tokens this
// test controls — including the malicious ones, which is the point.
func mint(t *testing.T, key *rsa.PrivateKey, kid, alg string, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(map[string]string{"alg": alg, "typ": "JWT", "kid": kid})
	p, _ := json.Marshal(claims)
	seg := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p)
	sum := sha256.Sum256([]byte(seg))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return seg + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func cacheWith(kid string, pub *rsa.PublicKey) *keyCache {
	kc := newKeyCache("", &http.Client{})
	kc.keys = map[string]*rsa.PublicKey{kid: pub}
	kc.lastFetched = time.Now()
	return kc
}

func goodClaims() map[string]any {
	now := time.Now().Unix()
	return map[string]any{
		"iss": "https://auth.example.test/realms/homelab",
		"aud": "oidc-demo",
		"azp": "oidc-demo",
		"sub": "abc-123",
		"exp": now + 300,
		"iat": now,
	}
}

func setup(t *testing.T) (*rsa.PrivateKey, *keyCache) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key, cacheWith("k1", &key.PublicKey)
}

const iss = "https://auth.example.test/realms/homelab"

func TestValidTokenIsAccepted(t *testing.T) {
	key, kc := setup(t)
	c, err := verifyIDToken(mint(t, key, "k1", "RS256", goodClaims()), kc, iss, "oidc-demo", "")
	if err != nil {
		t.Fatalf("expected acceptance, got %v", err)
	}
	if c.Subject != "abc-123" {
		t.Fatalf("subject = %q", c.Subject)
	}
}

// A token signed by a key we do not trust must fail, even though it is a
// perfectly well-formed JWT with correct claims.
func TestForeignSignerIsRejected(t *testing.T) {
	_, kc := setup(t)
	attacker, _ := rsa.GenerateKey(rand.Reader, 2048)
	_, err := verifyIDToken(mint(t, attacker, "k1", "RS256", goodClaims()), kc, iss, "oidc-demo", "")
	if err == nil {
		t.Fatal("a token signed by an untrusted key was accepted")
	}
}

// The algorithm-confusion cases. "none" asks us to skip verification; "HS256"
// asks us to verify a symmetric MAC with a key the attacker also has.
func TestAlgorithmConfusionIsRejected(t *testing.T) {
	key, kc := setup(t)
	for _, alg := range []string{"none", "HS256", "RS512", ""} {
		if _, err := verifyIDToken(mint(t, key, "k1", alg, goodClaims()), kc, iss, "oidc-demo", ""); err == nil {
			t.Fatalf("alg %q was accepted", alg)
		}
	}
}

// A valid token issued to a DIFFERENT client of the same realm. Signature is
// genuine; it is simply not for us.
func TestWrongAudienceIsRejected(t *testing.T) {
	key, kc := setup(t)
	c := goodClaims()
	c["aud"] = "some-other-app"
	c["azp"] = "some-other-app"
	if _, err := verifyIDToken(mint(t, key, "k1", "RS256", c), kc, iss, "oidc-demo", ""); err == nil {
		t.Fatal("a token for another client was accepted")
	}
}

func TestWrongIssuerIsRejected(t *testing.T) {
	key, kc := setup(t)
	c := goodClaims()
	c["iss"] = "https://evil.test/realms/homelab"
	if _, err := verifyIDToken(mint(t, key, "k1", "RS256", c), kc, iss, "oidc-demo", ""); err == nil {
		t.Fatal("a token from another issuer was accepted")
	}
}

func TestExpiredTokenIsRejected(t *testing.T) {
	key, kc := setup(t)
	c := goodClaims()
	c["exp"] = time.Now().Add(-10 * time.Minute).Unix()
	if _, err := verifyIDToken(mint(t, key, "k1", "RS256", c), kc, iss, "oidc-demo", ""); err == nil {
		t.Fatal("an expired token was accepted")
	}
}

func TestNonceMismatchIsRejected(t *testing.T) {
	key, kc := setup(t)
	c := goodClaims()
	c["nonce"] = "from-a-different-login"
	if _, err := verifyIDToken(mint(t, key, "k1", "RS256", c), kc, iss, "oidc-demo", "the-one-we-sent"); err == nil {
		t.Fatal("a token bound to another authorization request was accepted")
	}
}

// aud is "a string OR an array of strings" in the spec. The array form is the
// one a naive struct field silently fails to parse.
func TestAudienceAcceptsBothForms(t *testing.T) {
	key, kc := setup(t)
	c := goodClaims()
	c["aud"] = []string{"other-app", "oidc-demo"}
	if _, err := verifyIDToken(mint(t, key, "k1", "RS256", c), kc, iss, "oidc-demo", ""); err != nil {
		t.Fatalf("array-form audience rejected: %v", err)
	}
}

func TestUnknownKidDoesNotHammerTheProvider(t *testing.T) {
	key, kc := setup(t)
	// lastFetched is recent, so an unknown kid must fail fast rather than
	// triggering a refetch on every forged token.
	_, err := verifyIDToken(mint(t, key, "unknown-kid", "RS256", goodClaims()), kc, iss, "oidc-demo", "")
	if err == nil || !strings.Contains(err.Error(), "no key for kid") {
		t.Fatalf("expected a fast failure on unknown kid, got %v", err)
	}
}

func TestMalformedTokensAreRejected(t *testing.T) {
	_, kc := setup(t)
	for _, bad := range []string{"", "a.b", "a.b.c.d", "not-a-token", "...."} {
		if _, err := verifyIDToken(bad, kc, iss, "oidc-demo", ""); err == nil {
			t.Fatalf("malformed token %q was accepted", bad)
		}
	}
}

func TestJWKParsesModulusAndExponent(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	k := jwk{
		Kty: "RSA",
		N:   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}
	pub, err := k.rsaPublicKey()
	if err != nil {
		t.Fatal(err)
	}
	if pub.N.Cmp(key.N) != 0 || pub.E != key.E {
		t.Fatal("reconstructed key does not match the original")
	}
}

func TestIndentXMLHandlesSAMLShape(t *testing.T) {
	in := []byte(`<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol"><Assertion>x</Assertion></samlp:Response>`)
	out, err := indentXML(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "\n") {
		t.Fatal("expected indented output")
	}
}

// The property that matters: indenting must not REWRITE the document. A SAML
// signature covers exact bytes, so a namespace prefix that survives display but
// not re-serialisation is the classic way to break signature validation.
// encoding/xml fails this test, which is why it is not used.
func TestIndentXMLPreservesNamespacePrefixesAndTags(t *testing.T) {
	in := []byte(`<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ` +
		`xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="a1">` +
		`<saml:Issuer>https://auth.example.test</saml:Issuer>` +
		`<saml:Assertion ID="a2"><saml:NameID Format="persistent">akos</saml:NameID></saml:Assertion>` +
		`</samlp:Response>`)
	out, err := indentXML(in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, must := range []string{
		"<samlp:Response", "</samlp:Response>", "<saml:Issuer>", "<saml:Assertion ID=\"a2\">",
		`xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol"`,
		`Format="persistent"`, "akos",
	} {
		if !strings.Contains(got, must) {
			t.Fatalf("indenting lost %q\n---\n%s", must, got)
		}
	}
	// No invented attributes, and no prefix collapsed into a default namespace.
	if strings.Contains(got, "_xmlns") {
		t.Fatalf("indenting invented an attribute:\n%s", got)
	}
	// Every original tag must survive byte-identically once whitespace is removed.
	strip := func(s string) string {
		return strings.NewReplacer("\n", "", " ", "", "\t", "").Replace(s)
	}
	if strip(got) != strip(string(in)) {
		t.Fatalf("document changed.\nin:  %s\nout: %s", strip(string(in)), strip(got))
	}
}
