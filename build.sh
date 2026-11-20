#!/bin/sh
# Build the React frontend, then the Go binary that embeds it.
set -e
cd "$(dirname "$0")"
VERSION=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null | sed "s/^v//" || echo dev)}
(cd frontend && { [ -d node_modules ] || npm ci --no-audit --no-fund; } && ./node_modules/.bin/tsc --noEmit && ./node_modules/.bin/vite build --logLevel warn)
touch backend/web/.gitkeep
(cd backend && go vet ./... && go test ./... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o ../kubepilot .)
echo "OK: $(ls -lh kubepilot | awk '{print $5}') → ./kubepilot"
