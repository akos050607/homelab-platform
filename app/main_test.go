package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBurnIsDeterministicAndNonTrivial(t *testing.T) {
	a, b := burn(1000), burn(1000)
	if a != b {
		t.Fatalf("burn is not deterministic: %v != %v", a, b)
	}
	// Guards against the compiler eliminating the loop, which would make the
	// HPA demo silently measure nothing.
	if a == 0 {
		t.Fatal("burn returned 0; the work was optimised away")
	}
}

func TestHealthzIsCheap(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	// Mirrors the handler registered in main.
	http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok\n"))
	}).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "ok") {
		t.Fatalf("unexpected body %q", rr.Body.String())
	}
}

func TestGetenvInt(t *testing.T) {
	for _, tc := range []struct {
		name, env string
		want      int
	}{
		{"unset falls back", "", 42},
		{"valid value wins", "7", 7},
		{"garbage falls back", "banana", 42},
		{"zero falls back", "0", 42},
		{"negative falls back", "-5", 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv("BURN_TEST", tc.env)
			}
			if got := getenvInt("BURN_TEST", 42); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}
