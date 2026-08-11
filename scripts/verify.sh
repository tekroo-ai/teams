#!/bin/sh
set -eu

mkdir -p build/reports

go test -race ./...
go vet ./...

node CONTRACTS/tekroo.kernel.contracts/0.1.0/runner/validate-package.mjs \
  build/reports/contract-structure.json
node CONTRACTS/tekroo.kernel.contracts/0.1.0/runner/reference-runner.mjs \
  build/reports/reference-corpus.json
