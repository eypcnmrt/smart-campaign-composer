# Backend images: one build stage, two runtime images (composer and mock-provider).
#
#   docker build --target composer      -t scc-composer .
#   docker build --target mock-provider -t scc-mock-provider .
#
# The runtime images run as an unprivileged user and contain only the static binaries.

FROM golang:1.27 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-buildvcs=false
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/composer ./cmd/mock-provider ./cmd/soak

FROM alpine:3.22 AS runtime
RUN addgroup -S -g 10001 app && adduser -S -u 10001 -G app -H -s /sbin/nologin app
USER 10001:10001

FROM runtime AS composer
COPY --from=build /out/composer /out/soak /usr/local/bin/
ENTRYPOINT ["composer"]

FROM runtime AS mock-provider
COPY --from=build /out/mock-provider /usr/local/bin/
EXPOSE 8081
ENTRYPOINT ["mock-provider"]
