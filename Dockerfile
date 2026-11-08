# syntax=docker/dockerfile:1
# One multi-target Dockerfile for all services: one shared Go module layer and one web build
# (need.md N-ORC-009). Targets: api, worker, mockprovider (runtime) and gotest, webtest (make test).
ARG GO_IMAGE=golang:1.27.3-bookworm
ARG NODE_IMAGE=node:24-bookworm-slim
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot
# Must match the exact @playwright/test version in web/package.json (browsers are baked into the image).
ARG PLAYWRIGHT_IMAGE=mcr.microsoft.com/playwright:v1.65.0-noble

# ---------- web ----------
FROM ${NODE_IMAGE} AS web-deps
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund

FROM web-deps AS web-build
COPY web/ ./
COPY docs/contracts/testvectors /src/docs/contracts/testvectors
RUN npm run build

# Web unit tests. Dependencies are baked in, so the container runs without network.
FROM web-build AS webtest
CMD ["npm", "test"]

# `make e2e`: Playwright against the compose stack; joins the default network and reaches the API as api:8080.
FROM ${PLAYWRIGHT_IMAGE} AS e2e
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
ENV E2E_BASE_URL=http://api:8080 CI=1
CMD ["npx", "playwright", "test"]

# ---------- go ----------
FROM ${GO_IMAGE} AS go-deps
ENV GOTOOLCHAIN=local
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

FROM go-deps AS go-build
COPY . .
COPY --from=web-build /src/web/dist ./web/dist
ARG VERSION=dev
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -tags spa -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/mockprovider ./cmd/mockprovider

# Go tests (vet + race detector). Modules are baked in and the proxy is off, so no network is needed.
FROM go-deps AS gotest
ENV GOPROXY=off GOFLAGS=-mod=readonly
COPY . .
CMD ["sh", "-c", "go vet ./... && go test -race -count=1 ./..."]

# ---------- runtime images ----------
FROM ${RUNTIME_IMAGE} AS api
COPY --from=go-build /out/api /app/api
EXPOSE 8080
ENTRYPOINT ["/app/api"]
CMD ["serve"]

FROM ${RUNTIME_IMAGE} AS worker
COPY --from=go-build /out/worker /app/worker
EXPOSE 9091
ENTRYPOINT ["/app/worker"]

FROM ${RUNTIME_IMAGE} AS mockprovider
COPY --from=go-build /out/mockprovider /app/mockprovider
EXPOSE 8090
ENTRYPOINT ["/app/mockprovider"]
