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

# CGO_ENABLED=0 matches .goreleaser.yml, so the image ships the same statically
# linked binary as every other release artifact and the build stage's Alpine
# version does not have to track the runtime stage's.
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X github.com/bitmagnet-io/bitmagnet/internal/version.GitTag=$(git describe --tags --always --dirty)"

FROM alpine:3.20

RUN apk --update add \
    curl \
    iproute2-ss \
    && rm -rf /var/cache/apk/*

COPY --from=build /build/bitmagnet /usr/bin/bitmagnet

ENTRYPOINT ["bitmagnet"]
