# PR Review Guide: HYPERFLEET-534 - Helm Charts

## How to Review This PR

This PR is split into 3 commits for easier review. Review each commit independently.

---

## Commit 1: Documentation & Configuration Templates

### What Changed
- New `CONFIG_SCHEMA.md` documenting the adapter configuration format
- Minimal and full configuration templates
- Updated internal READMEs for config_loader and criteria packages

### Review Checklist
- [ ] Schema documentation is accurate and complete
- [ ] YAML examples are valid (`yamllint configs/`)
- [ ] Templates cover common deployment scenarios
- [ ] Internal READMEs are updated correctly

### How to Test
```bash
# Validate YAML syntax
find configs/ -name "*.yaml" -exec yamllint {} \;
```

---

## Commit 2: Helm Chart Infrastructure & Examples

### What Changed
- New JSON schema for values validation
- ConfigMap templates for adapter config and broker
- RBAC template for service account permissions
- Simplified deployment template using helpers
- 4 complete deployment examples

### Review Checklist
- [ ] `helm lint charts/` passes
- [ ] JSON schema validates correctly
- [ ] RBAC uses least privilege
- [ ] Each example renders correctly with `helm template`
- [ ] Helper functions are well-documented

### How to Test
```bash
# Lint the chart
helm lint charts/

# Template each example
helm template test charts/ -f charts/examples/example1-namespace/values.yaml
helm template test charts/ -f charts/examples/example2-job/values.yaml
helm template test charts/ -f charts/examples/example3-deployment/values.yaml
helm template test charts/ -f charts/examples/example4-manifestwork/values.yaml
```

---

## Commit 3: Build & Tests

### What Changed
- Dockerfile cleanup (removed embedded config comments)
- Makefile updates for new broker config structure
- Main README rewrite with clearer architecture
- Test files updated to match new config naming

### Review Checklist
- [ ] `make build` succeeds
- [ ] `make test` passes
- [ ] Docker image builds successfully
- [ ] README accurately describes the architecture

### How to Test
```bash
make build
make test
docker build -t hyperfleet-adapter:test .
```
