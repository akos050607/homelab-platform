# Multi-stage: the Go toolchain is ~800 MB and has no business shipping to a
# cluster. Only the compiled binary crosses into the final image.
FROM golang:1.23-alpine AS build

WORKDIR /src

# Copied separately so dependency download is cached independently of source
# changes. (No dependencies today — the layout is what matters when there are.)
COPY go.mod ./
RUN go mod download

COPY . .

RUN go vet ./... && go test ./...

ARG VERSION=dev
# CGO_ENABLED=0 produces a static binary, which is what lets the runtime image
# be distroless — there is no libc in there to link against.
# -trimpath strips local filesystem paths out of the binary; -s -w drop the
# symbol table and DWARF data.
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/demo .

# Distroless: no shell, no package manager, no busybox. If this container is
# ever compromised there is nothing in it to pivot with. `nonroot` runs as
# uid 65532 rather than root.
FROM gcr.io/distroless/static-debian12:nonroot

# Links the package to this repository in the GitHub UI and lets it inherit
# repository visibility.
LABEL org.opencontainers.image.source="https://github.com/akos050607/homelab-platform"
LABEL org.opencontainers.image.description="CPU-burning demo workload for HPA load testing"
LABEL org.opencontainers.image.licenses="MIT"

COPY --from=build /out/demo /demo

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/demo"]
