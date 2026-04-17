#!/usr/bin/env bash
set -euo pipefail

#execute from root repository folder
# --dry-run-output json \
go run ./cmd/adapter/main.go serve \
  --log-level debug \
  --dry-run-verbose \
  --config ./test/testdata/dryrun/dryrun-maestro-adapter-config.yaml \
  --task-config ./test/testdata/dryrun/maestro-delete/dryrun-maestro-delete-task-config.yaml \
  --dry-run-event ./test/testdata/dryrun/event.json \
  --dry-run-discovery ./test/testdata/dryrun/maestro-delete/dryrun-maestro-delete-discovery.json \
  --dry-run-api-responses ./test/testdata/dryrun/dryrun-delete-api-responses.json
