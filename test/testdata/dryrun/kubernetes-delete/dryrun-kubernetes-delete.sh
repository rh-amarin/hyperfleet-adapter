#!/usr/bin/env bash
set -euo pipefail

#execute from root repository folder
go run ./cmd/adapter/main.go serve \
  --debug-config=true \
  --dry-run-verbose \
  --config test/testdata/dryrun/dryrun-kubernetes-adapter-config.yaml \
  --task-config test/testdata/dryrun/kubernetes-delete/dryrun-kubernetes-delete-task-config.yaml \
  --dry-run-event test/testdata/dryrun/event.json \
  --dry-run-api-responses test/testdata/dryrun/dryrun-delete-api-responses.json \
  --dry-run-discovery test/testdata/dryrun/kubernetes-delete/dryrun-kubernetes-delete-discovery.json
