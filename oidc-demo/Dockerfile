# Multi-stage: the Go toolchain is ~800 MB and has no business shipping to a
# cluster. Only the compiled binary crosses into the final image.
FROM golang:1.23-alpine AS build

WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .

# Run inside the build so that an image which exists is an image whose tests
# passed. A separate CI job cannot promise that — anyone can build locally.
RUN go vet ./... && go test ./...

ARG VERSION=dev
# CGO_ENABLED=0 produces a static binary, which is what lets the runtime image
# be distroless — there is no libc in there to link against.
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/oidc-demo .

# Distroless: no shell, no package manager, no busybox. If this container is
# ever compromised there is nothing in it to pivot with.
#
# It matters more here than for the other workload: this process holds a client
# secret and mints session cookies. `nonroot` runs as uid 65532, and the base
# image carries the CA bundle this service needs to verify Keycloak's TLS.
FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.source="https://github.com/akos050607/homelab-platform"
LABEL org.opencontainers.image.description="OpenID Connect relying party: authorization code flow with PKCE against Keycloak"
LABEL org.opencontainers.image.licenses="MIT"

COPY --from=build /out/oidc-demo /oidc-demo

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/oidc-demo"]
