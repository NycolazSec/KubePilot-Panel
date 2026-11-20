# syntax=docker/dockerfile:1
# Multi-arch build: the frontend and the Go binary are built on the build platform,
# the binary is cross-compiled for the target platform (pure Go, no CGO).

FROM --platform=$BUILDPLATFORM node:26-alpine AS web
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN ./node_modules/.bin/tsc --noEmit && ./node_modules/.bin/vite build --logLevel warn

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=web /src/backend/web ./web
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/kubepilot .

FROM alpine:3.24
LABEL org.opencontainers.image.title="KubePilot" \
      org.opencontainers.image.description="Kubernetes-native game server panel" \
      org.opencontainers.image.source="https://github.com/NycolazSec/KubePilot-Panel" \
      org.opencontainers.image.licenses="AGPL-3.0-only"
COPY --from=build /out/kubepilot /usr/local/bin/kubepilot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/kubepilot"]
