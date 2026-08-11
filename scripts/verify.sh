#!/bin/sh
set -eu

mkdir -p build/reports

go test -race ./...
go vet ./...

go run ./cmd/core-mutation-report -output build/reports/core-mutations.json
go run ./cmd/core-mutation-report -verify build/reports/core-mutations.json

node CONTRACTS/tekroo.kernel.contracts/0.2.0/runner/validate-package.mjs \
  build/reports/contract-structure.json
node CONTRACTS/tekroo.kernel.contracts/0.2.0/runner/reference-runner.mjs \
  build/reports/reference-corpus.json
