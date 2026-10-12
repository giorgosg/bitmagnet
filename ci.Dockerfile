FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS ui-builder
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /ui
COPY webui/package.json webui/package-lock.json ./
RUN npm ci
COPY webui/ ./
RUN npm run build:optimize

# build app
FROM --platform=$BUILDPLATFORM golang:1.26.7-alpine3.23 AS app-builder
RUN apk add --no-cache git tzdata

ENV SERVICE=bitmagnet

WORKDIR /src

# Cache Go modules
COPY go.mod go.sum ./
RUN go mod download

COPY . ./
COPY --from=ui-builder /ui/public ./webui/public

ARG VERSION=dev
ARG REVISION=dev
ARG BUILDTIME
ARG TARGETOS TARGETARCH TARGETVARIANT

RUN --network=none \
export GOOS=$TARGETOS; \
export GOARCH=$TARGETARCH; \
[[ "$GOARCH" == "amd64" ]] && export GOAMD64=$TARGETVARIANT; \
[[ "$GOARCH" == "arm" ]] && [[ "$TARGETVARIANT" == "v6" ]] && export GOARM=6; \
[[ "$GOARCH" == "arm" ]] && [[ "$TARGETVARIANT" == "v7" ]] && export GOARM=7; \
echo $GOARCH $GOOS $GOARM$GOAMD64; \
CGO_ENABLED=0 go build -tags webui -ldflags "-s -w -X github.com/bitmagnet-io/bitmagnet/internal/version.GitTag=${VERSION}" -o /build/bitmagnet main.go

# build runner
FROM alpine:latest AS runner

LABEL org.opencontainers.image.source = "https://github.com/bitmagnet-io/bitmagnet"
LABEL org.opencontainers.image.licenses = "MIT"
LABEL org.opencontainers.image.base.name = "alpine:latest"

RUN apk --no-cache add ca-certificates curl tzdata jq iproute2-ss

COPY --link --from=app-builder /build/bitmagnet* /usr/local/bin/

ENTRYPOINT ["/usr/local/bin/bitmagnet"]
