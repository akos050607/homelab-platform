// A deliberately CPU-hungry HTTP service.
//
// It exists to be scaled: the handler burns measurable CPU per request so a
// HorizontalPodAutoscaler has something real to react to. It is the first-party
// replacement for registry.k8s.io/hpa-example, so that the load test on this
// platform exercises an image built by this repository's own pipeline.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

// Injected at build time with -ldflags "-X main.version=...".
var version = "dev"

// burn does floating-point work that the compiler cannot optimise away, because
// the result escapes into the response.
func burn(iterations int) float64 {
	var acc float64
	for i := 0; i < iterations; i++ {
		acc += math.Sqrt(float64(i)) * math.Sin(float64(i))
	}
	return acc
}

func main() {
	port := getenv("PORT", "8080")
	defaultIter := getenvInt("BURN_ITERATIONS", 2_000_000)

	mux := http.NewServeMux()

	// Liveness and readiness. Must stay cheap — a probe that competes with the
	// workload for CPU will fail exactly when the pod is busiest, and the
	// kubelet will restart a pod that is merely under load.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, version)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		iter := defaultIter
		if q := r.URL.Query().Get("iterations"); q != "" {
			if n, err := strconv.Atoi(q); err == nil && n > 0 {
				iter = n
			}
		}
		start := time.Now()
		result := burn(iter)
		host, _ := os.Hostname()

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "OK!\npod=%s\nversion=%s\niterations=%d\nelapsed=%s\nresult=%.4f\n",
			host, version, iter, time.Since(start), result)
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Kubernetes sends SIGTERM and then waits out terminationGracePeriodSeconds
	// before SIGKILL. Draining in-flight requests here is what makes a scale-down
	// or a rolling update invisible to a client mid-request.
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

	log.Printf("listening on :%s (version=%s, default iterations=%d)", port, version, defaultIter)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server error: %v", err)
	}
	<-idle
	log.Println("stopped")
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
		log.Printf("ignoring invalid %s=%q, using %d", key, v, fallback)
	}
	return fallback
}
