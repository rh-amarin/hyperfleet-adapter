# Maestro Two Resources Example

This example demonstrates how to manage two independent ManifestWorks with an
ordered deletion dependency using the HyperFleet Adapter framework.

## What This Example Does

Two ManifestWorks are created on the spoke cluster via Maestro:

| Resource | ManifestWork Name | Contents |
|---|---|---|
| `configMapManifestWork` | `mw-configmap-<clusterId>` | `ConfigMap/cluster-config` in the cluster namespace |
| `namespaceManifestWork` | `mw-namespace-<clusterId>` | `Namespace/<clusterId>` |

## Deletion Ordering

During cluster teardown (`is_deleting = true`):

1. **ConfigMap ManifestWork** is deleted first (`when: is_deleting`)
2. **Namespace ManifestWork** is deleted only once the ConfigMap ManifestWork is
   confirmed gone (`when: is_deleting && !resources.?configMapManifestWork.hasValue()`)

This ordering prevents the Namespace from being removed while the ConfigMap
still exists inside it, avoiding resource conflicts during Maestro's async cleanup.

```
Reconciliation 1 (is_deleting=true):
  → Delete configMapManifestWork         ✓ (condition met)
  → Delete namespaceManifestWork         ✗ (configMapManifestWork still present)

Reconciliation 2 (configMapManifestWork gone):
  → configMapManifestWork absent         -
  → Delete namespaceManifestWork         ✓ (condition met)

Reconciliation 3 (both gone):
  → Finalized = True                     → API hard-delete triggered
```

## Files

| File | Purpose |
|---|---|
| `adapter-config.yaml` | Adapter connection config (HyperFleet API, broker, Maestro) |
| `adapter-task-config.yaml` | Task workflow: params, preconditions, resources, status reporting |
| `adapter-task-resource-manifestwork-namespace.yaml` | ManifestWork template for the Namespace |
| `adapter-task-resource-manifestwork-configmap.yaml` | ManifestWork template for the ConfigMap |
| `values.yaml` | Helm values to deploy this example |

## Key Pattern: Deletion Dependency via Resource Presence Check

The delete condition for the Namespace ManifestWork uses optional chaining to check
whether the ConfigMap ManifestWork still exists at reconciliation time:

```yaml
lifecycle:
  delete:
    when:
      expression: "is_deleting && !resources.?configMapManifestWork.hasValue()"
```

`resources.?configMapManifestWork.hasValue()` returns `false` once Maestro confirms
the resource is gone, at which point the Namespace ManifestWork deletion is triggered.

## Differences from the `maestro` Example

| Aspect | `maestro` | `maestro-two-resources` |
|---|---|---|
| ManifestWorks | 1 (namespace + configmap together) | 2 (separate) |
| Deletion | Single delete when `is_deleting` | Ordered: configmap first, namespace second |
| Applied condition | 1 ManifestWork | Both ManifestWorks must be applied |
| Deleted condition | 1 ManifestWork absent | Both ManifestWorks absent |
| Finalized condition | 1 ManifestWork absent + guards | Both ManifestWorks absent + guards |
