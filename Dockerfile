FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS ui-builder
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /ui
COPY webui/package.json webui/package-lock.json ./
RUN npm ci
COPY webui/ ./
RUN npm run build:optimize

FROM golang:1.26.7-alpine3.23 AS build

RUN apk --update add \
    gcc \
    musl-dev \
    git

RUN mkdir /build

WORKDIR /build

COPY go.mod go.sum /build/

RUN go mod download

COPY . /build
COPY --from=ui-builder /ui/public /build/webui/public

# CGO_ENABLED=0 matches .goreleaser.yml, so the image ships the same statically
# linked binary as every other release artifact and the build stage's Alpine
# version does not have to track the runtime stage's.
RUN CGO_ENABLED=0 go build -tags webui -ldflags "-s -w -X github.com/bitmagnet-io/bitmagnet/internal/version.GitTag=$(git describe --tags --always --dirty)"

FROM alpine:3.20

RUN apk --update add \
    curl \
    iproute2-ss \
    && rm -rf /var/cache/apk/*

COPY --from=build /build/bitmagnet /usr/bin/bitmagnet

ENTRYPOINT ["bitmagnet"]
