#!/bin/bash

VERSION="${FGLPKG_VERSION:-2.2.0}"
BUILD="${FGLPKG_BUILD:-$(date +%Y%m%d%H%M%S)}"
LDFLAGS="-s -w -X github.com/4js-mikefolcher/fglpkg/internal/cli.Version=${VERSION} -X github.com/4js-mikefolcher/fglpkg/internal/cli.Build=${BUILD}"

echo "Building fglpkg v${VERSION} (build ${BUILD})"

# Linux ARM
GOOS=linux GOARCH=arm64 go build -ldflags="${LDFLAGS}" -o ./bin/fglpkg-linux-arm64 ./cmd/fglpkg

# Linux Intel
GOOS=linux GOARCH=amd64 go build -ldflags="${LDFLAGS}" -o ./bin/fglpkg-linux-amd64 ./cmd/fglpkg

# Mac Apple Silicon
GOOS=darwin GOARCH=arm64 go build -ldflags="${LDFLAGS}" -o ./bin/fglpkg-darwin-arm64 ./cmd/fglpkg

# Mac Intel
GOOS=darwin GOARCH=amd64 go build -ldflags="${LDFLAGS}" -o ./bin/fglpkg-darwin-amd64 ./cmd/fglpkg

# Windows ARM
GOOS=windows GOARCH=arm64 go build -ldflags="${LDFLAGS}" -o ./bin/fglpkg-windows-arm64.exe ./cmd/fglpkg

# Windows Intel
GOOS=windows GOARCH=amd64 go build -ldflags="${LDFLAGS}" -o ./bin/fglpkg-windows-amd64.exe ./cmd/fglpkg

echo "Done. Built 6 binaries in ./bin/"
