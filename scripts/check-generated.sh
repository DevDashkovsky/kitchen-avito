#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
task_tmp=$(mktemp -d)
trap 'rm -rf "$task_tmp"' EXIT
cp internal/kitchen/api/openapi.gen.go "$task_tmp/server.go"
cp internal/establishment/kitchenapi/client.gen.go "$task_tmp/client.go"
go generate ./...
cmp "$task_tmp/server.go" internal/kitchen/api/openapi.gen.go
cmp "$task_tmp/client.go" internal/establishment/kitchenapi/client.gen.go
