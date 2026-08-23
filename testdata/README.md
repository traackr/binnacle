# Test fixtures

Sample binnacle configurations covering the config surface. Every fixture is
synthetic: chart names, releases, namespaces, and hostnames are invented, and
URLs use the reserved `example.com` / `example.invalid` domains. This is a
public repository, so fixtures MUST NOT be copied from a real deployment
configuration, and MUST NOT name real services, clusters, namespaces, registry
paths, or IAM roles.

New fixtures SHOULD carry a header comment saying which code path they exercise
and why, so a later reader can tell an intentional edge case from an accident.

## Positive fixtures

| File | Covers |
| --- | --- |
| `demo.yml` | The documented baseline: one chart, one repository, explicit states. |
| `default-state.yml` | Omitted `state` on both chart and repository, which MUST default to `present`. |
| `absent.yml` | `state: absent` on a chart, driving the uninstall path. |
| `without-repo.yml` | A chart named by absolute URL with no `repo`, so `ChartURL()` returns the name verbatim. |
| `camel-case-values.yml` | Shallow camelCase value keys. Regression guard for the viper key-lowercasing bug. |
| `deep-values.yml` | camelCase keys nested many levels deep, including inside lists. Catches a loader that preserves case at the top level but loses it further down. |
| `chart-url-variants.yml` | All four chart-reference forms `ChartURL()` accepts: repo reference, packaged `.tgz` path, unpacked directory, absolute URL. |
| `multi-chart.yml` | Several charts across namespaces with mixed states, plus a repository with `state: absent` that MUST be removed before others are added. |
| `kustomize-inline-patches.yml` | Kustomize post-renderer using inline `patch` strings, so it needs no companion files. Exercises full group/version/kind targets, `labelSelector`, and patch `options`. |
| `kustomize/config.yml` | Kustomize post-renderer using file references. See below. |

## Negative fixtures

These MUST fail to load. A test asserting success against one of them is a bug
in the test.

| File | Why it fails |
| --- | --- |
| `invalid.yml` | Not YAML. |
| `unmarshallable.yml` | `charts` is a mapping where a list is required. |
| `unknown-key.yml` | `namesapce` (a typo for `namespace`) and an unknown `retries` key. A loader that silently drops these would deploy a release into the wrong namespace, so strict decoding MUST reject them. |

## The `kustomize/` directory

`SetupKustomize` resolves `resources` and `patches[].path` relative to the
directory holding the config file, then copies each into a temporary working
directory using only its basename. The companion files therefore MUST sit beside
the config that references them, which is why this fixture gets its own
directory rather than living flat alongside the others.

| File | Role |
| --- | --- |
| `config.yml` | The binnacle config. Referenced by tests. |
| `extra-configmap.yml` | A manifest merged in via `kustomize.resources`. |
| `api-resources.yml` | A strategic-merge patch referenced by `kustomize.patches[].path`. |

The two manifests are Kubernetes resources, not binnacle configs; a test that
loads them as binnacle configs is testing the wrong thing.
