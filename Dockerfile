# syntax=docker/dockerfile:1
# Multi-arch build: the frontend and the Go binary are built on the build platform,
# the binary is cross-compiled for the target platform (pure Go, no CGO).

FROM --platform=$BUILDPLATFORM node:26-alpine AS web
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN ./node_modules/.bin/tsc --noEmit && ./node_modules/.bin/vite build --logLevel warn

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=web /src/backend/web ./web
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/kubepilot .

# tcpcat (https://tcpcat.io): the firewall agent's exposure check runs it against the other machines (exposure.go).
# The release archive is checked against its published SHA-256 before anything is extracted.
FROM alpine:3.24 AS tcpcat
ARG TARGETARCH TCPCAT_VERSION=1.7.0
ARG TCPCAT_SHA256_AMD64=ef0c41e157086048c8dacb49fe4e0b36846d3cd812f2ef0836a86d7c35d3aaeb
ARG TCPCAT_SHA256_ARM64=350dad0c6a1b75ffcf214e4e925e75d886f766c5eecb42016e235a6116d1e8b5
RUN set -e; \
    case "$TARGETARCH" in amd64) sum=$TCPCAT_SHA256_AMD64 ;; arm64) sum=$TCPCAT_SHA256_ARM64 ;; *) echo "no tcpcat build for $TARGETARCH" >&2; exit 1 ;; esac; \
    wget -qO /tmp/tcpcat.tar.gz "https://github.com/NycolazSec/tcpcat/releases/download/v${TCPCAT_VERSION}/tcpcat_${TCPCAT_VERSION}_linux_${TARGETARCH}.tar.gz"; \
    echo "$sum  /tmp/tcpcat.tar.gz" | sha256sum -c -; \
    tar -xzf /tmp/tcpcat.tar.gz -C /tmp tcpcat; \
    install -m 0755 /tmp/tcpcat /tcpcat

FROM alpine:3.24
# nft: used only by the firewall agent (`kubepilot shield`, chart value shield.enabled); so is tcpcat
RUN apk add --no-cache nftables
COPY --from=tcpcat /tcpcat /usr/local/bin/tcpcat
LABEL org.opencontainers.image.title="KubePilot" \
      org.opencontainers.image.description="Kubernetes-native game server panel" \
      org.opencontainers.image.source="https://github.com/NycolazSec/KubePilot-Panel" \
      org.opencontainers.image.licenses="AGPL-3.0-only"
COPY --from=build /out/kubepilot /usr/local/bin/kubepilot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/kubepilot"]
