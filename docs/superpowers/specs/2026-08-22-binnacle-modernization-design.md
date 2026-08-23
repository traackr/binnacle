# Binnacle Modernization Design

Date: 2026-08-22
Status: Approved

## Context

Binnacle is an opinionated wrapper around Helm: it reads a YAML config describing
repositories and chart releases, then shells out to `helm` for `sync`, `diff`,
`template`, and `status`. It is ~1600 lines of Go on cobra + viper + logrus.

`employee-platform`'s `traackr-cli` is the reference for how Traackr builds Go
CLIs today: urfave/cli v3, `charm.land/lipgloss/v2`, release-please, strict
yaml.v3 config loading, shell completions. This design brings binnacle to that
standard, deviating only where binnacle's execution context demands it.

## Verified constraints

These were confirmed against the consuming repositories, not assumed. They are
the boundary conditions every change below MUST respect.

### Binnacle is a CI tool first

The primary consumer is `infra-platform/kubernetes-apps/jenkins/Jenkinsfile.deploy`,
which runs binnacle inside the `build-binnacle` container
(`ENTRYPOINT ["binnacle"]`). Invocations are exactly:

```
binnacle diff     -c <path> -- --kubeconfig <kubeconfig>
binnacle sync     -c <path> -- --kubeconfig <kubeconfig>
binnacle status   -c <path> -- --kubeconfig <kubeconfig>
binnacle template -c <path> -- --kubeconfig <kubeconfig> > /dev/null
```

Consequences:

- The `--` separator is load-bearing. Everything after it MUST reach `helm`
  verbatim.
- `-c` MUST remain required and keep its short form.
- `--loglevel` is not used by any CI caller, so its implementation MAY change
  freely, but every level name it accepts today MUST keep parsing.
- Output is read as Jenkins build log, never by a human at a TTY.

### The release artifact contract is rigid

`infra-platform/images/dockr/build-binnacle/<ver>/Dockerfile` hardcodes:

```
ENV BINNACLE_BASE_URL="https://github.com/Traackr/binnacle/releases/download/${BINNACLE_VERSION}"
ENV BINNACLE_TAR_FILE="binnacle-linux_${TARGETARCH}.tar.gz"
```

The git tag is a bare version (`1.0.1`) with **no** `v` prefix, and the asset
name carries no version. Both MUST be preserved, so `infra-platform` needs no
coordinated change.

### The `-lxc` targets are dead weight

`scripts/build.sh` passes `-tags "lxc"`, but no Go file in the repo carries an
`lxc` build tag, so `binnacle-linux_<arch>-lxc.tar.gz` is byte-identical to the
non-lxc asset. Nothing in `infra-platform` or `application-platform` references
the `-lxc` assets. They MUST be dropped.

### lipgloss v2 does not downsample at Render time

In lipgloss v2, `Style.Render()` always emits ANSI. Downsampling happens only at
the writer: `lipgloss.Writer` wraps `colorprofile.NewWriter(w, os.Environ())`,
which honors `NO_COLOR`, `CLICOLOR_FORCE`, `TERM=dumb`, and TTY detection.

`traackr-cli` writes through a plain `fmt.Fprintln` on a raw `os.Stderr`, so it
leaks raw ANSI when piped — verified empirically:

```
$ traackr-cli --version | cat -v
^[[1;38;5;61mtraackr-cli^[[m ^[[1;38;5;35mdev^[[m (^[[38;5;244mcommit: none, built: unknown^[[m)
```

This is harmless for a human-driven CLI and unacceptable for one that runs in
Jenkins. Binnacle MUST route all styled output through a `colorprofile.Writer`.
This is the one place binnacle deliberately improves on the reference rather
than copying it.

## Goals

1. Versioning and releases automated from Conventional Commits on merge.
2. urfave/cli v3 in place of cobra, with shell completions.
3. lipgloss-styled output that is legible to a human and inert in CI.
4. Remove viper, and with it the class of bug patched in `d37f928`.

## Non-goals

Each of these was considered and rejected for a stated reason.

| Rejected | Reason |
| --- | --- |
| `bubbletea` / `bubbles` / `huh` | Interactive widgets need a TTY. Binnacle has no interactive flow — it reads a file and shells out to helm. |
| `glamour` (and `glow`, which is a binary, not a library) | No markdown surface in binnacle worth a `chroma`-sized dependency. Revisit if a `binnacle docs` command is ever wanted. |
| `charmbracelet/log` | Depends on lipgloss **v1**, which would link both v1 and v2 into the binary. Stdlib `log/slog` instead. |
| `charmbracelet/fang` | Cobra-only starter kit; binnacle is leaving cobra. |
| Structural re-rendering of helm output | Parsing helm's output into a model couples binnacle to helm's format across versions. Line-oriented decoration is used instead. |
| Renaming `master` to `main` | A public repo's default-branch rename invalidates existing clones and external refs. Tracked as an open item, not folded in. |

## Delivery: a four-PR stack

Each PR bases on the previous one. PR 1 touches no Go code and can land alone.

```
docs/modernization-spec          this document
  └─ PR 1  release automation + drop lxc      (no Go changes)
       └─ PR 2  cobra -> urfave/cli v3 + completions
            └─ PR 3  drop viper
                 └─ PR 4  lipgloss + slog
```

---

## PR 1 — Release automation, drop lxc

### Single-workflow release

release-please pushing a tag with the default `GITHUB_TOKEN` does **not** trigger
`on: push: tags:` workflows — GitHub filters that to prevent recursion.
`employee-platform` works around it with a GitHub App token because it must fan
out to per-package deploy workflows.

Binnacle is a single-package repo, so it MUST instead do both halves in one
workflow, gated on release-please's own output. No GitHub App installation on the
`Traackr` org is required.

```yaml
on:
  push:
    branches: [master]
  workflow_dispatch:
    inputs:
      tag: { description: "Tag to (re)publish", required: true }

jobs:
  release-please:
    if: github.event_name == 'push'
    # outputs: release_created, tag_name

  build-and-upload:
    needs: release-please
    # `needs` on a skipped job forces this one to skip too unless the
    # condition explicitly re-admits the dispatch path.
    if: |
      needs.release-please.outputs.release_created == 'true' ||
      github.event_name == 'workflow_dispatch'
    # ref: inputs.tag on dispatch, else needs.release-please.outputs.tag_name
    # checkout @ ref -> mise -> test -> build -> upload assets
```

The `workflow_dispatch` input is retained as an escape hatch to re-publish assets
for an existing tag without deleting and recreating it. Note that
`always()`-style conditions MUST NOT be used to admit that path, as they would
also let the job run when `release-please` fails outright.

### Files

| File | Change |
| --- | --- |
| `release-please-config.json` | New. `"include-v-in-tag": false`, single root package, `"release-type": "go"`. |
| `.release-please-manifest.json` | New, seeded `{".": "1.0.1"}`. Becomes the sole source of version truth. |
| `VERSION` | Deleted. |
| `scripts/build.sh` | Reads the version from the manifest via `jq`, mirroring `employee-platform`'s `.mise/tasks/build`. Targets reduce to `darwin_amd64 darwin_arm64 linux_amd64 linux_arm64 windows_amd64`. |
| `.mise.toml` | Add `jq` so local builds resolve the version the same way CI does. |
| `.github/workflows/release.yml` | Rewritten per above. |
| `.github/workflows/commit-lint.yml` | New. Conventional Commits check, `on: pull_request` only. It MUST NOT live in `test.yml`, which also runs `on: push`, where there is no reliable commit range to validate. |
| `CHANGELOG.md` | release-please takes over. Legacy hand-written entries below `## [0.8.0]` are left in place. |

### Conventional Commits enforcement

A ~20 line bash step in the PR workflow validating each commit subject against
`^(feat|fix|chore|docs|refactor|test|ci|build|perf|style|revert)(\(.+\))?!?: .+`,
run over the PR's commit range
(`git log --format=%s origin/${{ github.base_ref }}..HEAD`). Per the dependency
ladder this MUST NOT pull in a Node toolchain (commitlint) for a regex.

Binnacle has no lefthook setup and this design does not add one; the check runs
in CI only.

### Deliberately not changed

`scripts/build.sh` carries two no-ops that are noted but left alone, as removing
them is unrelated to this goal: `-extldflags '-static'` and the mingw `CC`/`CXX`
assignments are both inert under `CGO_ENABLED=0`. The `windows_amd64` target is
likewise retained absent evidence nobody consumes it.

---

## PR 2 — cobra to urfave/cli v3, plus completions

### Structure

`cmd/root.go` is 424 lines mixing three unrelated concerns: flag wiring, helm
process execution, and kustomize post-renderer setup. The migration splits them.

```
main.go                  cli.Command construction, handleRunErr,
                         ldflags -> main.{version,commit,date}
internal/commands/       commands.go (Deps, BuildCommands)
                         sync.go diff.go template.go status.go
internal/helm/           RunHelmCommand, Result, syncRepositories,
                         getCurrentRepositories, repoExists,
                         PluginInstalled, ReleaseExists
internal/kustomize/      SetupKustomize, SetupBinnacleWorkingDir
config/                  unchanged in this PR
```

`config/` MUST stay a public package. `Traackr/binnacle` is a public repository
and moving it under `internal/` would break any external importer.

### Test seam

`internal/helm` MUST expose its process execution as a package-level variable so
tests can substitute it, following the pattern the reference CLI uses for
`describeCluster` in `internal/commands/kube.go`:

```go
var runHelm = runHelmCommand   // substituted in tests
```

Without this seam the `--` passthrough assertion in the testing table below is
not implementable without a real `helm` on PATH, and that assertion is the CI
contract most easily broken by this migration.

### ldflags change

Version stamping moves from
`-X github.com/Traackr/binnacle/cmd.VERSION` / `.GITCOMMIT`
to `-X main.version` / `-X main.commit` / `-X main.date`, matching the reference
CLI. `scripts/build.sh` MUST be updated in the same PR.

### Flags

- `--config` / `-c`: `Required: true`, `Sources: cli.EnvVars("BINNACLE_CONFIG")`.
- `--loglevel`: `Value: "info"`, `Sources: cli.EnvVars("BINNACLE_LOGLEVEL")`.
- Trailing `--` passthrough resolves via `cmd.Args().Slice()`, preserving the
  Jenkins invocation exactly.

### Completions

`EnableShellCompletion: true` plus `ConfigureShellCompletionCommand` yields
`binnacle completion {bash,zsh,fish}` for free. Beyond the free subcommand and
flag completion, `-c` gets a config-path completer.

A `completions` mise task is ported from `employee-platform/.mise/tasks/completions`,
including its guard for urfave/cli < v3.9.0, which emitted malformed fish
templates (`%!_(string=...)`, urfave/cli#2285).

### Error handling

Adopt the reference CLI's model, which the org CLAUDE.md documents:

- Plain errors returned from an Action are printed by `handleRunErr` with a
  styled prefix and exit 1.
- `cli.Exit(msg, code)` only where a non-1 exit code is genuinely needed.
- `cli.Exit("", code)` MUST NOT be used.

Full styling of these paths arrives in PR 4; PR 2 wires the plumbing.

---

## PR 3 — Drop viper

### The problem

`config.LoadAndValidateFromViper` currently:

1. Parses the config with `viper.UnmarshalExact`, which lowercases every key.
2. Re-reads the same file from disk with yaml.v3 (`reparseChartValues`) to repair
   the case-sensitive Helm value keys viper corrupted.
3. Joins the two results by **chart document order** — a positional join.

Alongside that:

- `cleanupMapValue` / `cleanupInterfaceMap` handle `map[interface{}]interface{}`,
  which is a yaml.v2 artifact that yaml.v3 never produces. Dead code.
- `validateConfig` is `return nil`, despite the loader being named
  `...AndValidate`.

`d37f928` patched the symptom. Removing viper removes the cause.

### The replacement

Adopt the reference CLI's loader verbatim in shape —
`employee-platform/cmd/traackr-cli/internal/config/loader.go`, whose
`decodeStrictYAML` is already in production:

```go
dec := yaml.NewDecoder(bytes.NewReader(data))
dec.KnownFields(true)   // recovers what viper.UnmarshalExact provided
```

`KnownFields(true)` restores unknown-key rejection, so dropping
`UnmarshalExact` loses nothing.

One deliberate difference: `traackr-cli` loads config it embeds itself, so its
`Loader` wraps an `embed.FS`. Binnacle loads a user-supplied file at an arbitrary
path, so its loader takes the path directly. Same decoder, different filesystem
root. Keeping the `fs.FS` seam where practical lets config tests run hermetically
rather than round-tripping through `testdata/`.

### Scope

- Delete `reparseChartValues`, the positional join, `cleanupMapValue`,
  `cleanupInterfaceMap`, `cleanupInterfaceArray`.
- Give `validateConfig` a real body: required fields per chart and repository,
  and a `state` value check.
- `mapstructure` struct tags become `yaml` tags.
- Drop `github.com/spf13/viper` from `go.mod`; the indirect
  `github.com/go-viper/mapstructure/v2` goes with it.
- `testdata/camel-case-values.yml` and the `d37f928` guard test MUST still pass
  unchanged. They are the regression contract for this PR.

---

## PR 4 — lipgloss and slog

### Structure

```
internal/ui/
  styles.go       Traackr brand palette + Styles struct (ported from traackr-cli)
  logger.go       Logger writing through colorprofile.Writer
  helm_lines.go   Decorate(styles, line) string
  summary.go      end-of-run summary via lipgloss/v2/table
  version.go      RenderVersion
```

Dependencies added: `charm.land/lipgloss/v2` and
`github.com/charmbracelet/colorprofile` (promoted from indirect to direct).
`lipgloss/v2/table` is a subpackage, not a separate module.

### Stream discipline

This is a hard rule, not a preference.

| Command | stdout | stderr |
| --- | --- | --- |
| `template` | raw YAML, byte-exact, never styled | styled diagnostics |
| `sync` | decorated helm lines | styled diagnostics |
| `status` | decorated helm lines | styled diagnostics |
| `diff` | decorated diff hunks | styled diagnostics |

`binnacle template` output is a data stream consumed downstream (`> /dev/null` in
Jenkins today, piped to `kubectl`/`kustomize` elsewhere). It MUST NOT be styled
under any color profile, and manifest integrity MUST NOT depend on TTY detection.

All binnacle diagnostics move to stderr. Anything currently printed to stdout by
binnacle itself (rather than by helm) moves with them.

### Line decoration

`Decorate` is line-oriented and non-destructive:

- It matches known helm line shapes: `STATUS:`, `REVISION:`, `NAME:`,
  `LAST DEPLOYED:`, `NAMESPACE:`, `+`/`-` diff hunks, and
  `Release "x" has been upgraded`.
- Any line it does not recognize MUST be returned verbatim. A line MUST NEVER be
  dropped, reordered, or reflowed.

The consequence is that a helm version bump degrades the output to "less pretty",
never to "lost output". Tests are table-driven against captured real helm output
fixtures in `testdata/`.

The unconditional `--color` binnacle passes to `helm diff` is removed, so
`NO_COLOR` becomes honorable end to end.

### Summary table

After `sync` and `status`, a `lipgloss/v2/table` summary of chart, namespace,
release, action, and result. This addresses the actual pain in Jenkins deploy
logs: identifying which of N charts failed inside thousands of lines of helm
output. It renders to stderr, alongside the other diagnostics.

### logrus to slog

`logrus` is in maintenance mode. Replace with stdlib `log/slog`, level driven by
`--loglevel`.

All six logrus level names MUST keep parsing. `slog` has no fatal or panic level,
so `panic` and `fatal` map to `slog.LevelError`. No CI caller passes `--loglevel`
today, but silently rejecting a previously valid value is a needless break.

Human-facing status lines go through `ui.Logger` (the `OK` / `Error` / `Warn` /
`Hint` prefixes), not through slog. slog carries debug and trace diagnostics only.

---

## Testing

| PR | Verification |
| --- | --- |
| 1 | Existing `go test ./...` on both OS matrix legs. Release workflow exercised by `workflow_dispatch` against an existing tag before the first automated release. Asset name and tag shape asserted by eye against the `build-binnacle` Dockerfile's expectations. |
| 2 | Existing tests MUST pass unmoved where possible. Add a test asserting `--` passthrough reaches the helm arg list intact, since that is the CI contract most easily broken. Golden-file test on `completion bash|zsh|fish` output being non-empty and free of `%!`. |
| 3 | `testdata/camel-case-values.yml` and the `d37f928` guard test pass unchanged. New tests for unknown-key rejection and the `validateConfig` rules. |
| 4 | Table-driven `Decorate` tests over captured helm output fixtures, asserting unrecognized lines pass through byte-identical. A test asserting `template` stdout is byte-identical with and without `NO_COLOR`, and with a forced color profile. |

## Risks

| Risk | Mitigation |
| --- | --- |
| A styling change corrupts `binnacle template` output and a bad manifest reaches a cluster | Stream discipline is structural, not conditional: `template` stdout never passes through the styling layer at all. Byte-identity test. |
| urfave/cli parses the Jenkins `--` invocation differently from cobra | Explicit passthrough test in PR 2. Verified invocation forms recorded above. |
| A helm version bump breaks line decoration | `Decorate` returns unrecognized lines verbatim by construction. |
| release-please's first PR proposes an unexpected version | `.release-please-manifest.json` is seeded at the current `1.0.1`, so the first release is a normal increment from a known point. |
| Dropping viper changes config acceptance for a real config in `kubernetes-apps` | `KnownFields(true)` matches `UnmarshalExact`'s strictness. Before merging PR 3, run `binnacle template` against every binnacle file in `infra-platform/kubernetes-apps/` and diff the output against the pre-change binary. |

## Open items

These are tracked, not resolved, and none block the stack.

1. **`master` vs `main`.** The default branch is `master`; the release workflow
   targets it. A rename is a clean standalone change if wanted, but is not folded
   into this work.
2. **`scripts/build.sh` no-ops.** `-extldflags '-static'` and the mingw
   `CC`/`CXX` assignments are inert under `CGO_ENABLED=0`.
3. **`windows_amd64` target.** Retained; no evidence either way about consumers.
4. **`vhs` / `freeze` as mise dev tools.** Scripted terminal recordings for the
   README would suit a change whose point is output legibility. Zero runtime cost.
   Deferred as documentation work.
5. **`glamour`.** Revisit only if a `binnacle docs` command becomes wanted.
