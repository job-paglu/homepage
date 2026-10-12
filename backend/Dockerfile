# Job Paglu API — multi-stage build producing a static binary on distroless.
#
# NOT THE PRODUCTION PATH. Production is Vercel's Go runtime, which builds from
# backend/go.mod and cmd/server/main.go and needs no container (DECISIONS D-20).
# This file is kept for two reasons that are worth more than it costs: it is how
# `docker compose` runs the API locally, and it documents a complete, portable
# deployment for any host that takes a container — which is what makes D-20
# reversible rather than a one-way door.
#
# Why static + distroless rather than alpine: the final image has no shell, no
# package manager and no libc, so a remote-code-execution bug has nothing to
# pivot to. It also starts fast, which matters on any scale-to-zero host.

# ---------------------------------------------------------------- build stage
FROM golang:1.25-alpine AS build

WORKDIR /src

# Dependencies first so an unchanged go.mod/go.sum keeps this layer cached.
#
# go.sum is REQUIRED, not globbed. An earlier version used `go.sum*` so that a
# checkout without one would still build — but that is precisely the behaviour
# worth refusing: without a committed go.sum, `go mod download` re-resolves and
# records whatever the proxy serves, so the image can contain module versions
# nothing in the repository pins. That is the same guarantee `npm ci` exists to
# protect on the frontend, and it should not be weaker here.
#
# `go mod download` with a go.sum present verifies every module against it and
# fails on a mismatch. If this COPY fails because go.sum is absent, the fix is
# `go mod tidy` and committing the result — not relaxing this line.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# vet BEFORE building. A vet failure is cheap to produce and the build is not,
# so failing first costs one compile instead of three.
RUN go vet ./...

# CGO off is what makes the binary static. -trimpath strips local paths out of
# panic traces; -s -w drops the symbol table and DWARF (~30% smaller image).
ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && go build -trimpath -ldflags="-s -w" -o /out/adminctl ./cmd/adminctl

# ---------------------------------------------------------------- final stage
# static-debian12 carries /etc/ssl/certs/ca-certificates.crt, which pgx needs to
# verify Neon's TLS certificate. It carries nothing else.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/server   /server
COPY --from=build /out/adminctl /adminctl

USER nonroot:nonroot

# Every host injects PORT; the default keeps `docker run` usable without it.
ENV PORT=8080
EXPOSE 8080

ENTRYPOINT ["/server"]
