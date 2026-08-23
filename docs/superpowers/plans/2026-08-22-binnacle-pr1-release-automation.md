# Binnacle PR 1: Release Automation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Automate binnacle's versioning and releases from Conventional Commits on merge to `main`, and stop publishing the dead `-lxc` build artifacts.

**Architecture:** release-please maintains a release PR on every push to `main`; when that PR merges it creates the tag and GitHub Release, and a dependent job in the same workflow attaches the built binaries. Both halves live in one workflow because a tag pushed by release-please with the default `GITHUB_TOKEN` does not trigger `on: push: tags:` workflows — GitHub filters that to prevent recursive runs — so a separate tag-triggered workflow would silently never fire. Version becomes a single value in `.release-please-manifest.json`, read by `scripts/build.sh`, replacing the hand-edited `VERSION` file.

**Tech Stack:** GitHub Actions, `googleapis/release-please-action@v4`, `jdx/mise-action@v4`, `gh` CLI, `jq`, bash. No Go code changes in this PR.

**Spec:** `docs/superpowers/specs/2026-08-22-binnacle-modernization-design.md`

**Branch:** `feat/release-automation`, based on `test/sample-config-fixtures`.

## Global Constraints

These are contract facts verified against the consuming repositories. Violating
any of them breaks a downstream consumer, not just a test.

- **Git tags MUST be a bare version with no `v` prefix** (`1.0.2`, never `v1.0.2`). `infra-platform/images/dockr/build-binnacle/<ver>/Dockerfile` builds its download URL as `releases/download/${BINNACLE_VERSION}` where `BINNACLE_VERSION: 1.0.1` comes from `config.yml`.
- **Release asset names MUST stay `binnacle-<os>_<arch>.tar.gz`** with no version in the filename. The same Dockerfile hardcodes `BINNACLE_TAR_FILE="binnacle-linux_${TARGETARCH}.tar.gz"`.
- **`SHA256SUM.txt` MUST continue to be published** alongside the tarballs, as it is today for tag `1.0.1`.
- **No Go source file may be modified in this PR.** The ldflags still target `github.com/Traackr/binnacle/cmd.VERSION`; retargeting to `main.version` belongs to PR 2.
- **The default branch is `main`.** The rename from `master` landed before this PR; `origin/HEAD` points at `main`.
- **Go version is pinned at `1.26.2`** in `.mise.toml`. Do not change it.
- Text files MUST end with a newline.
- Commit subjects MUST be 72 characters or fewer, imperative mood, no trailing period.

---

### Task 1: Drop the dead `-lxc` build targets

`scripts/build.sh` passes `-tags "lxc"` for two targets, but no Go file in the
repo carries an `lxc` build tag, so `binnacle-linux_amd64-lxc.tar.gz` is
*functionally* identical to `binnacle-linux_amd64.tar.gz`. Nothing in
`infra-platform` or `application-platform` references the `-lxc` assets.

They are **not** byte-identical, and cannot be: Go 1.18+ stamps build settings
into `runtime/debug.BuildInfo`, so the `-lxc` binary carries an extra
`build -tags=lxc` line. Compare symbol tables, not hashes.

**Files:**
- Modify: `scripts/build.sh:50` (release target list), `scripts/build.sh:72-86` (the two `-lxc` case arms)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: a release target list of exactly `darwin_amd64 darwin_arm64 linux_amd64 linux_arm64 windows_amd64`. Task 3's workflow depends on `mise run build` with `TARGETS=release` producing exactly those five `pkg/binnacle-*.tar.gz` files plus `pkg/SHA256SUM.txt`.

- [ ] **Step 1: Prove the `-lxc` binary contains no different code**

This is the evidence for the commit message. Do NOT use sha256: Go embeds the
`-tags` value in the binary's build metadata, so the hashes always differ even
when no code does. Compare the symbol tables and the build metadata instead.

```bash
cd /Users/tyrantkhan/traackr/platform-northstar/binnacle
TARGETS="linux_amd64 linux_amd64-lxc" mise run build

# a. Symbol tables: the real test. Same symbols == same code.
go tool nm pkg/linux_amd64/binnacle     | awk '{print $2, $3}' | sort > /tmp/nm-plain.txt
go tool nm pkg/linux_amd64-lxc/binnacle | awk '{print $2, $3}' | sort > /tmp/nm-lxc.txt
diff /tmp/nm-plain.txt /tmp/nm-lxc.txt && echo "SYMBOLS IDENTICAL ($(wc -l < /tmp/nm-plain.txt) symbols)"

# b. Build metadata: the only expected difference is the tags line.
go version -m pkg/linux_amd64/binnacle     > /tmp/bi-plain.txt
go version -m pkg/linux_amd64-lxc/binnacle > /tmp/bi-lxc.txt
diff /tmp/bi-plain.txt /tmp/bi-lxc.txt
```

Expected: (a) prints `SYMBOLS IDENTICAL` with a symbol count around 8497 and no
diff output. (b) shows exactly two differing lines — the leading filename in the
header, and one added `build -tags=lxc` line.

STOP and report BLOCKED if the symbol tables differ, or if (b) shows any
difference beyond the filename and the `-tags=lxc` line. Either would mean the
`lxc` tag actually changes the build and this task's premise is wrong.

- [ ] **Step 2: Confirm no Go file carries the `lxc` build tag**

Run:

```bash
grep -rn "lxc" --include="*.go" . ; echo "exit=$?"
```

Expected: no matches, `exit=1`.

- [ ] **Step 3: Remove the two `-lxc` case arms**

Delete these two blocks from `scripts/build.sh` entirely:

```bash
    "linux_amd64-lxc")
      echo "==> Building linux amd64 with lxc..."
      CGO_ENABLED=0 GOOS="linux" GOARCH="amd64" \
        go build -ldflags "$STATIC $EXTLDFLAGS" -o "pkg/linux_amd64-lxc/$PACKAGE" -tags "lxc"
      ;;
```

```bash
    "linux_arm64-lxc")
      echo "==> Building linux arm64 with lxc..."
      CGO_ENABLED=0 GOOS="linux" GOARCH="arm64" \
        go build -ldflags "$STATIC $EXTLDFLAGS" -o "pkg/linux_arm64-lxc/$PACKAGE" -tags "lxc"
      ;;
```

- [ ] **Step 4: Shorten the release target list**

Replace line 50:

```bash
  targets="darwin_amd64 darwin_arm64 linux_amd64 linux_amd64-lxc linux_arm64 linux_arm64-lxc windows_amd64"
```

with:

```bash
  targets="darwin_amd64 darwin_arm64 linux_amd64 linux_arm64 windows_amd64"
```

- [ ] **Step 5: Verify a full release build produces exactly five tarballs**

Run:

```bash
mise run clean
TARGETS=release GENERATE_PACKAGES=true mise run build
ls pkg/*.tar.gz | wc -l          # expect 5
ls pkg/ | grep lxc ; echo "lxc_grep_exit=$?"   # expect no output, exit=1
ls pkg/SHA256SUM.txt              # expect the file to exist
```

Expected: 5 tarballs, no `lxc` anywhere, `SHA256SUM.txt` present. Asset names
MUST read `binnacle-darwin_amd64.tar.gz`, `binnacle-darwin_arm64.tar.gz`,
`binnacle-linux_amd64.tar.gz`, `binnacle-linux_arm64.tar.gz`,
`binnacle-windows_amd64.tar.gz`.

- [ ] **Step 6: Confirm the Go test suite is untouched**

Run: `go test ./...`
Expected: PASS. No Go file changed, so this is a regression guard only.

- [ ] **Step 7: Commit**

```bash
git add scripts/build.sh
git commit -m "$(cat <<'MSG'
build: drop dead lxc release targets

build.sh passed -tags "lxc" for two targets, but no Go file in the repo
carries an lxc build tag, so the -lxc binaries expose exactly the same
symbols as the plain linux ones -- verified before removal by comparing
`go tool nm` output, which matched on all 8497 symbols. They were never
byte-identical: Go stamps `-tags=lxc` into the build metadata, which
`go version -m` reports as the sole difference. Nothing in
infra-platform or application-platform downloads the -lxc assets.

Release now publishes five tarballs instead of seven.
MSG
)"
```

---

### Task 2: Make `.release-please-manifest.json` the single version source

`VERSION` is hand-edited today and read in exactly one place,
`scripts/build.sh:15`. release-please owns the version from here on, so the
manifest becomes the source of truth and `VERSION` is deleted rather than left
to drift.

**Files:**
- Create: `release-please-config.json`
- Create: `.release-please-manifest.json`
- Delete: `VERSION`
- Modify: `scripts/build.sh:15` (version resolution)
- Modify: `.mise.toml` (add `jq`)

**Interfaces:**
- Consumes: the five-target release list from Task 1.
- Produces: `.release-please-manifest.json` with the shape `{".": "<semver>"}`. Task 3's workflow reads the version with `jq -r '.["."] // empty' .release-please-manifest.json`; that exact jq expression and file path are the contract between these tasks.

- [ ] **Step 1: Create the release-please config**

Create `release-please-config.json`:

```json
{
    "$schema": "https://raw.githubusercontent.com/googleapis/release-please/main/schemas/config.json",
    "include-v-in-tag": false,
    "packages": {
        ".": {
            "release-type": "go",
            "package-name": "binnacle",
            "changelog-path": "CHANGELOG.md"
        }
    }
}
```

`"include-v-in-tag": false` is load-bearing: it produces the bare `1.0.2` tag
that `infra-platform`'s Dockerfile URL requires. The default would be `v1.0.2`
and would break the image build.

- [ ] **Step 2: Seed the manifest at the currently released version**

Create `.release-please-manifest.json`:

```json
{
    ".": "1.0.1"
}
```

Seeding at `1.0.1` — the latest published release — means release-please's first
PR proposes a normal increment from a known point rather than restarting at
`0.1.0` or `1.0.0`.

- [ ] **Step 3: Verify the seeded version matches the latest real release**

Run:

```bash
gh release list --repo traackr/binnacle --limit 3
jq -r '.["."]' .release-please-manifest.json
```

Expected: the jq output (`1.0.1`) matches the most recent release tag. If they
differ, use the real latest tag in the manifest — a mismatch makes
release-please propose a version that already exists.

- [ ] **Step 4: Add `jq` to the pinned toolchain**

`build.sh` is about to depend on `jq`. GitHub's `ubuntu-latest` ships it, but
local builds MUST NOT depend on whatever the developer happens to have.

In `.mise.toml`, add `jq` to the `[tools]` table so it reads:

```toml
[tools]
go = "1.26.2"
helm = "4.2.4"
kustomize = "5.8.1"
jq = "1.7.1"
```

- [ ] **Step 5: Read the version from the manifest in build.sh**

Replace `scripts/build.sh` line 15:

```bash
VERSION="$(cat VERSION)"
```

with:

```bash
# The version comes from .release-please-manifest.json, which release-please
# rewrites when a release PR merges. It is the single source of truth; there is
# deliberately no VERSION file that could drift from it.
MANIFEST="$DIR/.release-please-manifest.json"
if [[ ! -f "$MANIFEST" ]]; then
  echo "==> ERROR: $MANIFEST not found" >&2
  exit 1
fi
VERSION="$(jq -r '.["."] // empty' "$MANIFEST")"
if [[ -z "$VERSION" ]]; then
  echo "==> ERROR: no version recorded for path '.' in $MANIFEST" >&2
  exit 1
fi
```

Both failure branches exit non-zero with a message naming the file. A build that
silently stamped an empty version would produce a binary reporting `-<commit>`
as its version, which is worse than failing.

- [ ] **Step 6: Delete the VERSION file**

```bash
git rm VERSION
```

- [ ] **Step 7: Verify the built binary reports the manifest version**

Run:

```bash
mise run clean
mise run build
./bin/binnacle --version
```

Expected: output contains `1.0.1` followed by `-` and the short commit — the
same shape as before this change, because `RootCmd.Version` is still
`fmt.Sprintf("%s-%s", VERSION, GITCOMMIT)` and no Go file changed.

- [ ] **Step 8: Verify build.sh fails loudly with a broken manifest**

Run:

```bash
cp .release-please-manifest.json /tmp/manifest.bak
echo '{}' > .release-please-manifest.json
mise run build ; echo "exit=$?"
cp /tmp/manifest.bak .release-please-manifest.json
```

Expected: the build fails with
`==> ERROR: no version recorded for path '.' in ...` and a non-zero exit.
Confirm the manifest is restored afterward with
`jq -r '.["."]' .release-please-manifest.json` printing `1.0.1`.

- [ ] **Step 9: Commit**

```bash
git add release-please-config.json .release-please-manifest.json .mise.toml scripts/build.sh
git add -u VERSION
git commit -m "$(cat <<'MSG'
build: read version from release-please manifest

release-please owns the version from here on, so the hand-edited VERSION
file is replaced by .release-please-manifest.json as the single source of
truth. build.sh resolves it with jq and fails loudly if the manifest is
missing or has no entry, rather than stamping an empty version.

include-v-in-tag is false so tags stay bare (1.0.2, not v1.0.2), which is
what infra-platform's build-binnacle Dockerfile download URL expects.
MSG
)"
```

---

### Task 3: Replace the tag-triggered release workflow

**Files:**
- Modify: `.github/workflows/release.yml` (full rewrite)

**Interfaces:**
- Consumes: the five-tarball output of Task 1, and the `jq -r '.["."] // empty' .release-please-manifest.json` version contract from Task 2.
- Produces: nothing consumed by later tasks. Task 4 is independent.

- [ ] **Step 1: Understand what the current workflow gets wrong**

Read `.github/workflows/release.yml`. It triggers on `push: tags: ['*']`, which
requires a human to hand-edit `VERSION` and push a tag. After Task 2 there is no
`VERSION` file to edit, and release-please's own tag push would not trigger it
anyway.

- [ ] **Step 2: Write the new workflow**

Replace the entire contents of `.github/workflows/release.yml` with:

```yaml
name: Release

# release-please owns versioning. On every push to main it maintains a release
# PR; when that PR merges it creates the tag and the GitHub Release, and
# build-and-upload attaches the binaries to the release it just created.
#
# Both halves live in one workflow deliberately. A tag pushed by release-please
# with the default GITHUB_TOKEN does NOT trigger `on: push: tags:` workflows --
# GitHub filters that to prevent recursive runs -- so a separate tag-triggered
# release workflow would silently never fire. Gating on release-please's own
# output avoids needing a GitHub App token to work around that.
on:
  push:
    branches: [main]
  workflow_dispatch:
    inputs:
      tag:
        description: "Existing tag to re-publish assets for (e.g. 1.0.2)"
        required: true
        type: string

# Least privilege: a read-only floor, and each job elevates only what it needs.
permissions:
  contents: read

concurrency:
  group: ${{ github.workflow }}
  cancel-in-progress: false

jobs:
  release-please:
    if: github.event_name == 'push'
    runs-on: ubuntu-latest
    # Manages the release PR and creates the tag + GitHub Release.
    permissions:
      contents: write
      pull-requests: write
    outputs:
      releases_created: ${{ steps.release.outputs.releases_created }}
    steps:
      - name: Maintain release PR / cut release
        id: release
        uses: googleapis/release-please-action@v4
        with:
          config-file: release-please-config.json
          manifest-file: .release-please-manifest.json

  build-and-upload:
    needs: release-please
    # GitHub ANDs an implicit success() onto any job `if:` that does not contain
    # a status-check function -- always(), success(), failure(), cancelled().
    # A job with `needs:` is therefore skipped whenever its dependency is
    # skipped, no matter what its own condition says. On workflow_dispatch
    # release-please is SKIPPED (its own `if:` is false), so without a
    # status-check function this job would never run and the workflow would
    # report success while doing nothing.
    #
    # !cancelled() supplies that function without a bare always()'s downside of
    # running through a cancelled workflow. The explicit result check keeps what
    # always() alone would discard: no publishing when release-please failed.
    if: |
      !cancelled() &&
      needs.release-please.result != 'failure' &&
      (needs.release-please.outputs.releases_created == 'true' ||
       github.event_name == 'workflow_dispatch')
    runs-on: ubuntu-latest
    # Only needs to attach assets to a release that already exists.
    permissions:
      contents: write
    steps:
      # On the push path github.sha is the merge commit of the release PR --
      # the exact commit release-please tagged -- so its manifest holds the
      # version being released. On the dispatch path, check out the tag itself.
      - name: Checkout
        uses: actions/checkout@v7
        with:
          ref: ${{ github.event.inputs.tag || github.sha }}
          fetch-depth: 0

      - name: Set up mise (installs go/helm/kustomize/jq from .mise.toml)
        uses: jdx/mise-action@v4

      - name: Cache Go modules + build cache
        uses: actions/cache@v6
        with:
          path: |
            ~/go/pkg/mod
            ~/.cache/go-build
          key: ${{ runner.os }}-go-${{ hashFiles('**/go.sum') }}
          restore-keys: |
            ${{ runner.os }}-go-

      - name: Resolve version from manifest
        id: version
        env:
          INPUT_TAG: ${{ github.event.inputs.tag }}
        run: |
          set -euo pipefail

          version="$(jq -r '.["."] // empty' .release-please-manifest.json)"
          if [ -z "$version" ]; then
            echo "no version recorded for path '.' in .release-please-manifest.json" >&2
            exit 1
          fi

          # On the dispatch path the checked-out tag must agree with the
          # manifest at that tag. Without this check a typo in the input would
          # publish assets stamped with a different version than the tag they
          # are attached to.
          if [ -n "${INPUT_TAG:-}" ] && [ "$INPUT_TAG" != "$version" ]; then
            echo "tag '$INPUT_TAG' does not match manifest version '$version'" >&2
            exit 1
          fi

          echo "version=$version" >> "$GITHUB_OUTPUT"
          echo "Publishing binnacle $version"

      - name: Test
        run: mise run test

      - name: Build release artifacts
        run: mise run build
        env:
          TARGETS: release
          GENERATE_PACKAGES: true

      # gh is preinstalled on GitHub runners, so this needs no third-party
      # action in the release path. --clobber makes a re-publish idempotent.
      - name: Upload assets to the release
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          VERSION: ${{ steps.version.outputs.version }}
        run: |
          set -euo pipefail
          ls -1 pkg/binnacle-*.tar.gz pkg/SHA256SUM.txt
          gh release upload "$VERSION" \
            pkg/binnacle-*.tar.gz \
            pkg/SHA256SUM.txt \
            --clobber
```

- [ ] **Step 3: Validate the workflow YAML parses**

Run:

```bash
python3 -c "import yaml,sys; d=yaml.safe_load(open('.github/workflows/release.yml')); print(sorted(d['jobs'])); print('on:', sorted(d[True] if True in d else d['on']))"
```

Expected: `['build-and-upload', 'release-please']` and the trigger keys
`['push', 'workflow_dispatch']`. Note PyYAML parses the bare `on:` key as the
boolean `True`, which is why the expression above checks for both.

- [ ] **Step 4: Assert the guard conditions are present**

Run:

```bash
# Every guard below appears BOTH in the condition and in the comment that
# documents it, so a raw `grep -c` counts the documentation too. Filter comment
# lines and count only functional occurrences. (Two earlier versions of this
# check asserted raw counts and both false-positived on the workflow's own
# comments -- first on always(), then on !cancelled().)
functional() {
  grep -n "$1" .github/workflows/release.yml | grep -v ':[[:space:]]*#'
}

functional "releases_created == 'true'"
functional "github.event_name == 'workflow_dispatch'"
functional '!cancelled()'
functional "needs.release-please.result != 'failure'"

# A bare always() MUST NOT appear in a condition. It appears only in comments.
if functional "always()" >/dev/null; then
  echo "FAIL: functional always() found"
else
  echo "ok: no functional always()"
fi
```

Expected: each of the first four prints exactly one line. The last prints
`ok: no functional always()`.

The `!cancelled()` condition MUST stay a `|` block scalar. `!` is YAML's tag
indicator, so the bare inline form `if: !cancelled() && ...` is a hard parse
error (`ScannerError: while scanning an anchor`). Do not collapse the condition
onto one line.

Do NOT delete the explanatory comment to make a grep pass, and do NOT replace
`!cancelled()` with a bare `always()`: that would run the upload job even when
release-please failed outright.

- [ ] **Step 5: Confirm the asset glob matches what Task 1 builds**

Run:

```bash
mise run clean
TARGETS=release GENERATE_PACKAGES=true mise run build
ls -1 pkg/binnacle-*.tar.gz pkg/SHA256SUM.txt
```

Expected: exactly the five tarballs and `SHA256SUM.txt` — the same list the
`gh release upload` step names. A glob that matches nothing would make
`gh release upload` fail the job, which is the desired direction of failure.

- [ ] **Step 6: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "$(cat <<'MSG'
ci: cut releases from release-please on merge to main

Replaces the tag-triggered release workflow, which required hand-editing
VERSION and pushing a tag by hand. release-please now maintains a release
PR and cuts the tag when it merges.

Build and upload live in the same workflow on purpose: a tag pushed by
release-please with the default GITHUB_TOKEN does not trigger
`on: push: tags:` workflows, so a separate one would never fire. Gating
on release-please's own output avoids needing a GitHub App token.

Uploads with gh rather than a third-party action, and keeps
workflow_dispatch as an escape hatch to re-publish an existing tag.
MSG
)"
```

---

### Task 4: Enforce Conventional Commits, and make dependabot comply

release-please derives the next version from commit subjects, so a
non-conventional subject on `main` is a silently missed release rather than a
cosmetic problem. This repo uses merge commits, so individual subjects do land
on `main`.

Dependabot currently emits `Bump actions/cache from 4 to 6`, which is not
conventional. Adding the lint without fixing dependabot would red-flag every
dependency PR, so both changes belong in this task.

**Files:**
- Create: `.github/workflows/commit-lint.yml`
- Modify: `.github/dependabot.yml` (add `commit-message` to both ecosystems)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Confirm the problem before fixing it**

Run:

```bash
git log --format='%s' --no-merges | grep -icE "^bump "
```

Expected: a non-zero count — 10 at time of writing. (An earlier draft of this
plan said 5. That number came from a command whose output was piped through
`head -5`, so the cap was mistaken for the total. Count with `grep -c`, never
with a truncated listing.) These are the dependabot subjects that a
conventional-commit lint would reject.

- [ ] **Step 2: Make dependabot emit conventional subjects**

In `.github/dependabot.yml`, add a `commit-message` block to **both** update
entries. The file becomes:

```yaml
version: 2
updates:
  # Go module dependencies — bundled into a single weekly PR
  - package-ecosystem: "gomod"
    directory: "/"
    schedule:
      interval: "weekly"
    # Without this, dependabot writes "Bump x from y to z", which fails the
    # conventional-commit check and gives release-please nothing to act on.
    commit-message:
      prefix: "chore"
      include: "scope"
    groups:
      go-dependencies:
        patterns:
          - "*"

  # GitHub Actions — bundled into a single weekly PR
  - package-ecosystem: "github-actions"
    directory: "/"
    schedule:
      interval: "weekly"
    commit-message:
      prefix: "chore"
      include: "scope"
    groups:
      github-actions:
        patterns:
          - "*"
```

`prefix: "chore"` with `include: "scope"` produces `chore(deps): bump ...`,
which satisfies the pattern in Step 3 and is a patch-level signal to
release-please.

- [ ] **Step 3: Add the commit-lint workflow**

Create `.github/workflows/commit-lint.yml`:

```yaml
name: Commit Lint

# release-please reads commit subjects to decide the next version, so a
# non-conventional subject on main is a missed release, not a style nit.
#
# pull_request only: on a bare push there is no reliable base to diff against,
# so there is no commit range to validate.
on:
  pull_request:

permissions:
  contents: read

jobs:
  conventional-commits:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout PR head
        uses: actions/checkout@v7
        with:
          fetch-depth: 0
          ref: ${{ github.event.pull_request.head.sha }}

      - name: Validate commit subjects
        env:
          BASE_REF: ${{ github.base_ref }}
        run: |
          set -euo pipefail

          # Fetch the base branch explicitly rather than assuming checkout left
          # a usable remote-tracking ref behind.
          git fetch --no-tags origin \
            "+refs/heads/${BASE_REF}:refs/remotes/origin/${BASE_REF}"

          pattern='^(feat|fix|chore|docs|refactor|test|ci|build|perf|style|revert)(\([a-z0-9._/-]+\))?!?: .+'
          failed=0

          # --no-merges: merging main into a branch brings along subjects that
          # were already validated on their own PR.
          subjects="$(git log --no-merges --format=%s "origin/${BASE_REF}..HEAD")"

          if [ -z "$subjects" ]; then
            echo "No non-merge commits to validate."
            exit 0
          fi

          while IFS= read -r subject; do
            [ -z "$subject" ] && continue
            if printf '%s' "$subject" | grep -qE "$pattern"; then
              printf 'ok    %s\n' "$subject"
            else
              printf 'FAIL  %s\n' "$subject"
              failed=1
            fi
          done <<< "$subjects"

          if [ "$failed" -ne 0 ]; then
            {
              printf '\n'
              printf 'Commit subjects must follow Conventional Commits:\n\n'
              printf '  <type>[(scope)][!]: <description>\n\n'
              printf 'Allowed types: feat fix chore docs refactor test ci build perf style revert\n\n'
              printf 'Examples:\n'
              printf '  feat: add kustomize post-renderer support\n'
              printf '  fix(config): preserve case-sensitive Helm value keys\n'
              printf '  feat!: drop Helm 2 support\n\n'
              printf 'release-please derives the next version from these, so a\n'
              printf 'non-conventional subject means a missed release.\n'
            } >&2
            exit 1
          fi
```

- [ ] **Step 4: Validate both YAML files parse**

Run:

```bash
python3 -c "
import yaml
for f in ['.github/workflows/commit-lint.yml', '.github/dependabot.yml']:
    d = yaml.safe_load(open(f))
    print(f, 'OK')
u = yaml.safe_load(open('.github/dependabot.yml'))['updates']
assert len(u) == 2, u
for e in u:
    assert e['commit-message']['prefix'] == 'chore', e
    print(e['package-ecosystem'], '->', e['commit-message'])
"
```

Expected: both files print `OK`, and both ecosystems report
`{'prefix': 'chore', 'include': 'scope'}`.

- [ ] **Step 5: Test the lint pattern against real subjects, good and bad**

The pattern is the whole value of this task, so test it directly rather than
trusting it. Run:

```bash
pattern='^(feat|fix|chore|docs|refactor|test|ci|build|perf|style|revert)(\([a-z0-9._/-]+\))?!?: .+'

echo "--- MUST PASS ---"
for s in \
  "feat: release linux arm64 binaries" \
  "fix: preserve case-sensitive Helm value keys through viper" \
  "fix(config): preserve case-sensitive Helm value keys" \
  "chore(deps): bump actions/cache from 4 to 6" \
  "feat!: drop Helm 2 support" \
  "build: drop dead lxc release targets" \
  "ci: cut releases from release-please on merge to main"; do
  printf '%s' "$s" | grep -qE "$pattern" && echo "ok   $s" || echo "WRONG-FAIL $s"
done

echo "--- MUST FAIL ---"
for s in \
  "Bump actions/cache from 4 to 6" \
  "Add --install and --three-way-merge flags to helm diff" \
  "binnacle 0.8.1 update kustomize to 5.4.2" \
  "feat missing colon" \
  "feat:" ; do
  printf '%s' "$s" | grep -qE "$pattern" && echo "WRONG-PASS $s" || echo "ok   $s"
done
```

Expected: every line begins with `ok`. Any `WRONG-FAIL` or `WRONG-PASS` means
the pattern is broken — fix it and re-run before committing. Note `"feat:"` with
no description MUST fail, which is what the trailing `.+` enforces.

- [ ] **Step 6: Verify this branch's own commits would pass**

Run:

```bash
pattern='^(feat|fix|chore|docs|refactor|test|ci|build|perf|style|revert)(\([a-z0-9._/-]+\))?!?: .+'
git log --no-merges --format=%s origin/main..HEAD | while IFS= read -r s; do
  printf '%s' "$s" | grep -qE "$pattern" && echo "ok   $s" || echo "FAIL $s"
done
```

Expected: every commit on `feat/release-automation` (including the spec and
fixtures commits inherited from the base branches) reports `ok`. A `FAIL` here
means this very PR would be blocked by the check it adds.

- [ ] **Step 7: Commit**

```bash
git add .github/workflows/commit-lint.yml .github/dependabot.yml
git commit -m "$(cat <<'MSG'
ci: validate conventional commits on pull requests

release-please derives the next version from commit subjects, so a
non-conventional subject on main is a silently missed release. This repo
merges rather than squashes, so individual subjects do reach main.

Dependabot is configured with a chore prefix in the same change: it
writes "Bump x from y to z" by default, so adding the check alone would
have failed every dependency PR.

Runs on pull_request only -- a bare push has no reliable base to diff
against, so there is no commit range to validate.
MSG
)"
```

---

### Task 5: Verify what can be verified before merge

The workflow cannot be fully proven by inspection: the `releases_created` output
name, the tag shape, and the asset names only resolve at runtime. But the
dispatch path **cannot** be rehearsed against the existing `1.0.1` tag, because
that tag's tree predates `.release-please-manifest.json` — the version-resolution
step would fail on a missing file. So this task verifies everything that is
testable pre-merge, and defers the rest to the first real release.

Note also that this PR's base is `test/sample-config-fixtures`, not `main`.
`Release` triggers on `push` to `main` only, so merging this stacked PR does not
cut a release; nothing fires until the whole stack reaches `main`.

**Files:**
- No file changes. This is a verification task.

**Interfaces:**
- Consumes: the workflows from Tasks 3 and 4, and the manifest contract from Task 2.
- Produces: a verification record on the PR.

- [ ] **Step 1: Push the whole stack, bases first**

A PR's base branch must exist on the remote before `gh pr create` will accept
it. None of the stack is pushed, so pushing only the topmost branch would make
the next step fail with "base branch not found". Push in dependency order:

```bash
git push -u origin docs/modernization-spec
git push -u origin test/sample-config-fixtures
git push -u origin feat/release-automation
```

Confirm all three landed before continuing:

```bash
for b in docs/modernization-spec test/sample-config-fixtures feat/release-automation; do
  git ls-remote --exit-code --heads origin "$b" >/dev/null 2>&1 \
    && echo "ok      $b" || echo "MISSING $b"
done
```

Expected: three `ok` lines. Anything `MISSING` means stop and re-push before
opening any PR.

- [ ] **Step 1b: Open the three stacked PRs**

Each PR's base is the branch below it, so each diff shows only its own work
rather than everything beneath it.

```bash
gh pr create --repo traackr/binnacle \
  --base main --head docs/modernization-spec \
  --title "docs: add binnacle modernization design spec" \
  --body "PR 1 of 4 in the modernization stack. Design spec only, no code."

gh pr create --repo traackr/binnacle \
  --base docs/modernization-spec --head test/sample-config-fixtures \
  --title "test: add diverse sample config fixtures" \
  --body "PR 2 of 4. Synthetic binnacle configs covering kustomize, deep camelCase values, all four chart-reference forms, and a strict-decoding rejection case. No behavior change."

gh pr create --repo traackr/binnacle \
  --base test/sample-config-fixtures --head feat/release-automation \
  --title "ci: automate releases with release-please, drop lxc targets" \
  --body "PR 3 of 4. See docs/superpowers/specs/2026-08-22-binnacle-modernization-design.md and docs/superpowers/plans/2026-08-22-binnacle-pr1-release-automation.md."
```

Note that `commit-lint.yml` only exists on `feat/release-automation`, so only
the third PR runs it. That is expected: the two lower PRs predate the workflow.

A later merge of the lower PRs will retarget the upper ones automatically — do
not close and reopen them.

- [ ] **Step 2: Confirm commit-lint runs and passes**

```bash
gh pr checks --repo traackr/binnacle --watch
```

Expected: `Commit Lint / conventional-commits` passes. Task 4 Step 6 should
already have caught any failure locally.

- [ ] **Step 3: Confirm the Release workflow did NOT run for this PR**

```bash
gh run list --repo traackr/binnacle --workflow Release --limit 5
```

Expected: no run for `feat/release-automation`. `Release` triggers only on
`push` to `main` and on `workflow_dispatch`. A run here means the trigger is
wrong and MUST be fixed before merge.

- [ ] **Step 4: Confirm commit-lint actually rejects a bad subject**

A check that never fails is worthless. Prove it fails, on a throwaway branch so
this PR's history stays clean.

```bash
git checkout -b tmp/commit-lint-negative
git commit --allow-empty -m "this subject is not conventional"
git push -u origin tmp/commit-lint-negative
gh pr create --repo traackr/binnacle --base feat/release-automation \
  --title "test: prove commit-lint rejects bad subjects" \
  --body "Throwaway. Expected to FAIL commit-lint. Close without merging."
gh pr checks --repo traackr/binnacle --watch ; echo "checks_exit=$?"
```

Expected: the commit-lint check **fails**, and the job log shows
`FAIL this subject is not conventional` plus the help text listing allowed
types. If it passes, the pattern or the commit range is wrong.

- [ ] **Step 5: Clean up the negative-test branch**

```bash
gh pr close --repo traackr/binnacle --delete-branch \
  "$(gh pr list --repo traackr/binnacle --head tmp/commit-lint-negative --json number --jq '.[0].number')"
git checkout feat/release-automation
git branch -D tmp/commit-lint-negative
```

Expected: the throwaway PR is closed and its branch deleted, locally and on the
remote. Confirm with `git branch -a | grep commit-lint-negative` returning
nothing.

- [ ] **Step 6: Record on the PR what was and was not verified**

```bash
gh pr comment --repo traackr/binnacle "$(gh pr view --json number --jq .number)" \
  --body "$(cat <<'BODY'
Verified pre-merge:
- commit-lint passes on this PR's history
- commit-lint fails on a deliberately non-conventional subject (throwaway PR, now closed)
- Release does not trigger on pull requests

NOT verifiable pre-merge, deferred to the first release on main:
- the `releases_created` output gate
- the bare tag shape (no `v` prefix)
- the published asset list dropping the -lxc tarballs

The dispatch path cannot be rehearsed against tag 1.0.1: that tree has no
.release-please-manifest.json, so version resolution would fail on a missing
file. First real exercise is the release-please PR after the stack lands.
BODY
)"
```

Adjust the text to what actually happened. If a step behaved differently, say so
plainly rather than restating the expected result.

- [ ] **Step 7: Write down the first-release checks for whoever merges the stack**

These MUST be run when the release-please PR is merged on `main`, and they are
the real proof. Add them to the PR description so they are not lost:

```bash
# 1. The tag has no v prefix
gh release list --repo traackr/binnacle --limit 1

# 2. The asset list dropped -lxc and kept the contract
gh release view <new-tag> --repo traackr/binnacle --json tagName,assets \
  --jq '{tagName, assets: [.assets[].name] | sort}'
#    MUST contain: binnacle-linux_amd64.tar.gz, binnacle-linux_arm64.tar.gz,
#                  binnacle-darwin_amd64.tar.gz, binnacle-darwin_arm64.tar.gz,
#                  binnacle-windows_amd64.tar.gz, SHA256SUM.txt
#    MUST NOT contain any *-lxc.tar.gz

# 3. The download URL infra-platform builds actually resolves
VER=<new-tag>
curl -sSfLI -o /dev/null \
  "https://github.com/Traackr/binnacle/releases/download/${VER}/binnacle-linux_amd64.tar.gz" \
  && echo "infra-platform download URL OK"
```

Check 3 is the one that matters most: it is the exact URL shape
`build-binnacle`'s Dockerfile constructs. If it 404s, the tag or asset naming
regressed and the image build will break on the next bump.

If any check fails, the fix is to correct the workflow and cut a patch release —
not to hand-edit assets on the published release.

---

## Notes for the implementer

**The `releases_created` output name.** release-please-action v4 in manifest mode
emits `releases_created` (plural) reliably. It also emits per-path outputs like
`.--tag_name` for the root package, but referencing those in a GitHub Actions
expression needs bracket syntax and is easy to get subtly wrong. This plan
deliberately avoids them: the gate uses only `releases_created`, and the version
comes from the manifest at the checked-out commit. If `releases_created` turns
out to be empty at runtime, print `${{ toJSON(needs.release-please.outputs) }}`
in a debug step and use whatever key is actually present — do not guess.

**Why `github.sha` is the right ref on the push path.** release-please creates
the tag on the merge commit of the release PR. That merge commit is what pushed
to `main`, so it is `github.sha` for this run. Checking it out gives both the
code being released and the manifest holding its version, with no need to read
a tag name from an output.

**What is explicitly out of scope.** No Go file changes; the ldflags keep
targeting `github.com/Traackr/binnacle/cmd.VERSION` until PR 2. The
`-extldflags '-static'` and mingw `CC`/`CXX` no-ops in `build.sh` stay as they
are — they are recorded as open items in the spec, and removing them is
unrelated to release automation. `CHANGELOG.md` is not reformatted; release-please
prepends to it and the legacy hand-written entries below `## [0.8.0]` stay.
