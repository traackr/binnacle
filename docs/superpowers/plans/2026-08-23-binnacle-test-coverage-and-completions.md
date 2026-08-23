# Binnacle Test Coverage and Working Completions — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Raise binnacle's test coverage from 11.18% to ~85%, make `binnacle completion` actually work, and stop polluting stdout — all on the existing cobra framework.

**Architecture:** Make the helm process call injectable with a one-line seam (`var RunHelmCommand = execHelm`), which unlocks unit tests for the 391 statements in `cmd/` that currently cannot be reached. Move config loading out of the global `cobra.OnInitialize` hook onto the four commands that actually read config, which fixes a known cobra limitation breaking `completion` and `help`. Then test the helm-argument construction of every command, since that is binnacle's actual job.

**Tech Stack:** Go 1.26.2, cobra (retained), viper (retained), logrus (retained), `getsentry/codecov-action` for the gate.

**Base:** `main` at `39a8bfa`. Branch: `feat/test-coverage-and-completions`.

## Decision reversal recorded

The merged spec (`docs/superpowers/specs/2026-08-22-binnacle-modernization-design.md`) specifies a
cobra → urfave/cli v3 migration, justified partly by wanting shell completions and partly by needing
a test seam. Both premises turned out to be wrong:

- **Cobra already ships completions.** `binnacle completion bash|zsh|fish|powershell` exists today
  with no code required. The urfave migration was not needed for this.
- **The seam is one line.** `RunHelmCommand` can become a package-level variable in place. The
  migration was not needed for testability either.

The migration is therefore dropped from the near-term plan. Task 4 corrects the spec so it does not
instruct a future reader to perform a migration that was deliberately abandoned.

## Global Constraints

- **The Jenkins invocation MUST keep working**: `binnacle <cmd> -c <path> -- --kubeconfig <path>`, for
  `sync`, `diff`, `template`, and `status`. Everything after `--` reaches helm verbatim.
- **Helm argument order MUST NOT change.** This is a test-and-fix PR, not a refactor. Note the
  existing inconsistency and preserve it: `status` appends the passthrough args *before*
  `--namespace`, while `sync`, `diff`, and `template` append them *last*. Tests pin current order.
- **`binnacle template` stdout MUST be pure manifest data.** No diagnostics on stdout for any command.
- **`config/` stays a public package.** `Traackr/binnacle` is a public repo.
- Release asset names (`binnacle-<os>_<arch>.tar.gz`), `SHA256SUM.txt`, and bare version tags are
  unchanged by this PR. Do not touch `.github/workflows/release.yml` or `release-please-config.json`.
- Go stays pinned at `1.26.2` in `.mise.toml`.
- **Patch coverage is gated at 90% on changed lines**, project at `auto` (no regression). Every task
  that adds code adds its tests in the same task.
- Text files MUST end with a newline.
- Commit subjects MUST be <= 72 chars, imperative, no trailing period, Conventional Commits format.
  The repo lints this on every PR and `main` now requires the check.

## Baseline

`mise run coverage` on `39a8bfa`: **11.18%**, 51 of 456 statements.

| package | statements | covered | % | share of total |
| --- | --- | --- | --- | --- |
| `cmd` | 391 | 11 | 2.8% | 85.7% |
| `config` | 64 | 40 | 62.5% | 14.0% |
| root (`main.go`) | 1 | 0 | 0% | 0.2% |

`cmd` is where essentially all the work is. `main.go`'s single statement and the real `exec.Command`
call inside `execHelm` are the irreducible remainder, which is why the target is ~85% and not 100%.

---

### Task 1: Make helm injectable and stop config loading from breaking non-config commands

Two changes that belong together: both are about `cmd/root.go` doing work globally that should be
scoped. The seam unlocks every later task, and the config-scoping fix is what makes `completion` work.

**Files:**
- Modify: `cmd/root.go` (the `RunHelmCommand` declaration, `init()`, `initConfig()`)
- Modify: `cmd/sync.go`, `cmd/diff.go`, `cmd/template.go`, `cmd/status.go` (add `PreRunE`)
- Test: `cmd/root_test.go` (create)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `var RunHelmCommand = execHelm` where `func execHelm(args ...string) (Result, error)`. Every later
    task substitutes `RunHelmCommand` in tests and restores it. Call sites are unchanged.
  - `func loadConfig() error` — the former `initConfig`, now returning an error instead of calling
    `log.Fatal`, wired as `PreRunE` on the four data commands.
  - `func withHelm(t *testing.T, fake func(args ...string) (Result, error))` test helper in
    `cmd/root_test.go`, used by Tasks 2 and 3. It swaps `RunHelmCommand` and registers
    `t.Cleanup` to restore it.

- [ ] **Step 1: Write the failing tests**

Create `cmd/root_test.go`:

```go
package cmd

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// withHelm swaps the package's helm executor for the duration of the test and
// records every invocation. Restores the real executor via t.Cleanup.
//
// Tasks 2 and 3 both use this; it is the only way to exercise the command code
// without a real helm binary on PATH.
func withHelm(t *testing.T, fake func(args ...string) (Result, error)) *[][]string {
	t.Helper()
	prev := RunHelmCommand
	var calls [][]string
	RunHelmCommand = func(args ...string) (Result, error) {
		calls = append(calls, args)
		return fake(args...)
	}
	t.Cleanup(func() { RunHelmCommand = prev })

	return &calls
}

// helmOK is the common fake: succeeds with the given stdout.
func helmOK(stdout string) func(...string) (Result, error) {
	return func(...string) (Result, error) {
		return Result{Stdout: stdout}, nil
	}
}

func TestRunHelmCommandIsSubstitutable(t *testing.T) {
	calls := withHelm(t, helmOK("fake output"))

	res, err := RunHelmCommand("version", "--short")
	if err != nil {
		t.Fatalf("substituted helm returned error: %v", err)
	}
	if res.Stdout != "fake output" {
		t.Errorf("Stdout = %q, want %q", res.Stdout, "fake output")
	}
	if len(*calls) != 1 {
		t.Fatalf("recorded %d calls, want 1", len(*calls))
	}
	if got := strings.Join((*calls)[0], " "); got != "version --short" {
		t.Errorf("recorded args = %q, want %q", got, "version --short")
	}
}

func TestLoadConfigReturnsErrorWithoutConfigFile(t *testing.T) {
	prev := cfgFile
	cfgFile = ""
	t.Cleanup(func() { cfgFile = prev })

	if err := loadConfig(); err == nil {
		t.Fatal("loadConfig() with no -c returned nil, want an error")
	}
}

func TestLoadConfigDoesNotWriteToStdout(t *testing.T) {
	// binnacle template's stdout is a manifest stream piped to kubectl, so no
	// command may write diagnostics there. Regression guard for the
	// `fmt.Println("Loaded config file:", ...)` that used to run for every
	// command.
	prev := cfgFile
	cfgFile = filepath.Join("..", "testdata", "demo.yml")
	// viper is a package-level singleton, so a config left loaded here would
	// leak into any later test that reads it.
	t.Cleanup(func() { cfgFile = prev; viper.Reset() })

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	realStdout := os.Stdout
	os.Stdout = w

	loadErr := loadConfig()

	w.Close()
	os.Stdout = realStdout

	var buf strings.Builder
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("reading captured stdout: %v", err)
	}
	if loadErr != nil {
		t.Fatalf("loadConfig() on a valid file: %v", loadErr)
	}
	if buf.Len() != 0 {
		t.Errorf("loadConfig() wrote %q to stdout, want nothing", buf.String())
	}
}

// TestCompletionWorksWithoutConfigFile builds the binary and runs it, because
// the bug being guarded is in cobra's command wiring, not in a function this
// package can call directly. The generated script must be usable, which means
// non-empty and free of diagnostic lines.
func TestCompletionWorksWithoutConfigFile(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary; skipped under -short")
	}

	bin := filepath.Join(t.TempDir(), "binnacle")
	build := exec.Command("go", "build", "-o", bin, "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building binnacle: %v\n%s", err, out)
	}

	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			cmd := exec.Command(bin, "completion", shell)
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			if err := cmd.Run(); err != nil {
				t.Fatalf("completion %s failed: %v\nstderr: %s", shell, err, stderr.String())
			}
			if stdout.Len() == 0 {
				t.Fatalf("completion %s produced no output", shell)
			}
			if strings.Contains(stdout.String(), "Loaded config file") {
				t.Errorf("completion %s script is polluted with a diagnostic line", shell)
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/ -run 'TestRunHelmCommandIsSubstitutable|TestLoadConfig|TestCompletionWorks' -v`

Expected: compile failure — `RunHelmCommand` is a func, not a variable, so it cannot be assigned;
and `loadConfig` does not exist. That compile error IS the failing state.

- [ ] **Step 3: Convert RunHelmCommand into a substitutable variable**

In `cmd/root.go`, rename the existing function and add the variable. Change the declaration from:

```go
// RunHelmCommand runs the given command against helm
func RunHelmCommand(args ...string) (Result, error) {
```

to:

```go
// RunHelmCommand runs the given command against helm.
//
// It is a variable rather than a function so tests can substitute the process
// call. Every command in this package funnels through it, so without this seam
// none of them can be exercised without a real helm binary on PATH.
var RunHelmCommand = execHelm

func execHelm(args ...string) (Result, error) {
```

No call site changes are needed: `RunHelmCommand(cmdArgs...)` still compiles.

- [ ] **Step 4: Turn initConfig into loadConfig, returning errors and logging to the logger**

Replace the whole `initConfig` function in `cmd/root.go` with:

```go
// loadConfig reads the Binnacle config file named by -c and initializes the
// logger. Wired as PreRunE on the commands that read config, rather than as a
// global cobra.OnInitialize hook: commands like completion, help and version
// need no config, and a global hook made them fail without -c.
func loadConfig() error {
	if cfgFile == "" {
		return errors.New("no configuration file specified; pass -c/--config")
	}

	viper.SetConfigFile(cfgFile)
	viper.AddConfigPath(".") // check current dir
	viper.AutomaticEnv()     // read in environment variables that match

	if err := viper.ReadInConfig(); err != nil {
		return fmt.Errorf("loading configuration file %q: %w", cfgFile, err)
	}

	// Initialize the logger for all commands to use.
	logLevel, err := logrus.ParseLevel(viper.GetString("loglevel"))
	if err != nil {
		return fmt.Errorf("parsing loglevel %q: %w", viper.GetString("loglevel"), err)
	}
	log.Level = logLevel

	// Deliberately the logger and not fmt.Println: binnacle template's stdout
	// is a manifest stream consumed by kubectl, so nothing may write
	// diagnostics there.
	log.Debugf("Loaded config file: %s", viper.ConfigFileUsed())

	return nil
}
```

Add `"errors"` to the imports if it is not already present.

- [ ] **Step 5: Stop registering the global hook and the required flag**

In `cmd/root.go`'s `init()`, delete these two lines:

```go
	cobra.OnInitialize(initConfig)
```

```go
	RootCmd.MarkFlagRequired("config")
```

The `cobra.OnInitialize(initConfig)` removal is the actual fix for the completion bug: `OnInitialize`
hooks run before every command's own logic, so `completion` was going through `initConfig` and hitting
its `log.Fatal` when no `-c` was given. (A required-flag-inheritance mechanism, spf13/cobra#2212, is
often blamed for this class of bug; it was tested and disproved here at this repo's pinned cobra
v1.10.2 — re-adding only `RootCmd.MarkFlagRequired("config")` leaves `completion zsh` working, while
re-adding only the `OnInitialize` hook reproduces the failure immediately. The `OnInitialize` hook was
the sole cause.) Requiredness moves to `loadConfig`, which only the commands that need config call.

Leave the `RootCmd.PersistentFlags()` declarations for `config` and `loglevel` exactly as they are,
including the `viper.BindPFlag` call.

- [ ] **Step 6: Wire loadConfig as PreRunE on the four data commands**

Each of `cmd/sync.go`, `cmd/diff.go`, `cmd/template.go`, `cmd/status.go` currently has a `PreRun`
that only logs. Change each to a `PreRunE` that loads config first. For `cmd/sync.go`, replace:

```go
	PreRun: func(cmd *cobra.Command, args []string) {
		syncCmdPreRun()
	},
```

with:

```go
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if err := loadConfig(); err != nil {
			return err
		}
		syncCmdPreRun()

		return nil
	},
```

Apply the identical shape to the other three, keeping each file's own `*CmdPreRun()` call:
`diffCmdPreRun()` in `cmd/diff.go`, `templateCmdPreRun()` in `cmd/template.go`, and
`statusCmdPreRun()` in `cmd/status.go`.

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./cmd/ -run 'TestRunHelmCommandIsSubstitutable|TestLoadConfig|TestCompletionWorks' -v`
Expected: PASS, including all three `TestCompletionWorksWithoutConfigFile` subtests.

- [ ] **Step 8: Verify the two bugs are actually fixed, end to end**

```bash
mise run clean && mise run build
echo "--- completion with no -c (was: fatal, exit 1) ---"
./bin/binnacle completion zsh | head -3
echo "lines: $(./bin/binnacle completion zsh | wc -l)"
echo "--- no diagnostic pollution ---"
./bin/binnacle completion zsh | grep -c "Loaded config file" || echo "clean"
echo "--- a config-reading command still errors clearly without -c ---"
./bin/binnacle status; echo "exit=$?"
```

Expected: the completion script is >100 lines and contains no `Loaded config file`; `binnacle status`
with no `-c` exits non-zero with a message naming `-c/--config`.

- [ ] **Step 9: Confirm the Jenkins invocation still works**

```bash
./bin/binnacle status -c testdata/demo.yml -- --kubeconfig /nonexistent 2>&1 | head -5
```

Expected: it reaches helm and fails on helm's terms (missing kubeconfig or missing helm), NOT on
argument parsing. A cobra usage error here means the `--` passthrough regressed.

- [ ] **Step 10: Full suite and coverage**

Run: `go test ./...` then `mise run coverage` and
`awk 'NR>1 { n=$(NF-1); t+=n; if ($NF>0) c+=n } END { printf "%.2f%%\n", 100*c/t }' coverage.out`

Expected: all tests pass; coverage is above the 11.18% baseline.

- [ ] **Step 11: Commit**

```bash
git add cmd/root.go cmd/sync.go cmd/diff.go cmd/template.go cmd/status.go cmd/root_test.go
git commit -m "$(cat <<'MSG'
fix: scope config loading to the commands that need it

binnacle completion exited 1 with "no configuration file specified" and
was therefore unusable. Two causes, both from doing config work globally:
a required persistent flag on root is inherited by the built-in completion
command, which cannot supply it (spf13/cobra#2212), and
cobra.OnInitialize ran initConfig for every command, where it called
log.Fatal.

Config loading moves to PreRunE on sync, diff, template and status.
completion, help and version no longer touch it, and the missing-config
error becomes a returned error rather than a fatal from a global hook.

initConfig also wrote "Loaded config file:" to stdout for every command,
which corrupted the generated completion script and prepended a non-YAML
line to binnacle template's manifest stream. It is now a debug log.

RunHelmCommand becomes a variable wrapping execHelm so tests can
substitute the process call. Call sites are unchanged.
MSG
)"
```

---

### Task 2: Test the helm plumbing in cmd/root.go

The functions that talk to helm and to the filesystem. This is the largest single coverage win after
the commands themselves, and `SetupKustomize` finally gets exercised by the `testdata/kustomize`
fixtures added earlier, which nothing has read until now.

**Files:**
- Test: `cmd/helm_test.go` (create)
- Test: `cmd/kustomize_test.go` (create)

**Interfaces:**
- Consumes: `withHelm(t, fake)` and `helmOK(stdout)` from `cmd/root_test.go` (Task 1), and
  `var RunHelmCommand = execHelm`.
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Write the helm-plumbing tests**

Create `cmd/helm_test.go`:

```go
package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/Traackr/binnacle/config"
)

func TestPluginInstalled(t *testing.T) {
	// helm plugin list emits a header row, which the parser must skip.
	const listOutput = "NAME\tVERSION\tDESCRIPTION\n" +
		"diff\t3.9.0\tPreview helm upgrade changes\n" +
		"secrets\t4.1.0\tSecrets management\n"

	tests := []struct {
		name   string
		plugin string
		want   bool
	}{
		{"installed plugin is found", "diff", true},
		{"second installed plugin is found", "secrets", true},
		{"absent plugin is not found", "s3", false},
		{"header row is not treated as a plugin", "NAME", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withHelm(t, helmOK(listOutput))

			got, err := PluginInstalled(tc.plugin)
			if err != nil {
				t.Fatalf("PluginInstalled(%q): %v", tc.plugin, err)
			}
			if got != tc.want {
				t.Errorf("PluginInstalled(%q) = %v, want %v", tc.plugin, got, tc.want)
			}
		})
	}
}

func TestPluginInstalledCallsHelmCorrectly(t *testing.T) {
	calls := withHelm(t, helmOK("NAME\tVERSION\n"))

	if _, err := PluginInstalled("diff"); err != nil {
		t.Fatalf("PluginInstalled: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("made %d helm calls, want 1", len(*calls))
	}
	if got := strings.Join((*calls)[0], " "); got != "plugin list" {
		t.Errorf("helm called with %q, want %q", got, "plugin list")
	}
}

func TestPluginInstalledPropagatesHelmError(t *testing.T) {
	withHelm(t, func(...string) (Result, error) {
		return Result{Stderr: "helm exploded"}, errors.New("exit status 1")
	})

	if _, err := PluginInstalled("diff"); err == nil {
		t.Fatal("PluginInstalled returned nil error when helm failed")
	}
}

func TestGetCurrentRepositories(t *testing.T) {
	const listOutput = "NAME\tURL\n" +
		"example\thttps://charts.example.com\n" +
		"thirdparty\thttps://charts-thirdparty.example.com\n"

	withHelm(t, helmOK(listOutput))

	repos, err := getCurrentRepositories()
	if err != nil {
		t.Fatalf("getCurrentRepositories: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("parsed %d repos, want 2", len(repos))
	}
	if repos[0].Name != "example" || repos[0].URL != "https://charts.example.com" {
		t.Errorf("repos[0] = %+v, want name=example url=https://charts.example.com", repos[0])
	}
	if repos[1].Name != "thirdparty" {
		t.Errorf("repos[1].Name = %q, want thirdparty", repos[1].Name)
	}
}

func TestRepoExists(t *testing.T) {
	current := []config.RepositoryConfig{
		{Name: "example", URL: "https://charts.example.com"},
	}

	tests := []struct {
		name          string
		repo          config.RepositoryConfig
		wantExists    bool
		wantFullMatch bool
	}{
		{
			name:          "exact match",
			repo:          config.RepositoryConfig{Name: "example", URL: "https://charts.example.com"},
			wantExists:    true,
			wantFullMatch: true,
		},
		{
			name:          "same name different url",
			repo:          config.RepositoryConfig{Name: "example", URL: "https://moved.example.com"},
			wantExists:    true,
			wantFullMatch: true,
		},
		{
			name:          "unknown repo",
			repo:          config.RepositoryConfig{Name: "other", URL: "https://other.example.com"},
			wantExists:    false,
			wantFullMatch: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exists, fullMatch := repoExists(tc.repo, current)
			if exists != tc.wantExists {
				t.Errorf("exists = %v, want %v", exists, tc.wantExists)
			}
			if fullMatch != tc.wantFullMatch {
				t.Errorf("fullMatch = %v, want %v", fullMatch, tc.wantFullMatch)
			}
		})
	}
}

func TestSyncRepositoriesAddsAndUpdates(t *testing.T) {
	// getCurrentRepositories runs first and reports nothing configured, so the
	// repo is added and a repo update follows because something changed.
	calls := withHelm(t, helmOK("NAME\tURL\n"))

	repos := []config.RepositoryConfig{
		{Name: "example", URL: "https://charts.example.com", State: config.StatePresent},
	}
	if err := syncRepositories(repos); err != nil {
		t.Fatalf("syncRepositories: %v", err)
	}

	var got []string
	for _, c := range *calls {
		got = append(got, strings.Join(c, " "))
	}
	joined := strings.Join(got, " | ")

	if !strings.Contains(joined, "repo add example https://charts.example.com") {
		t.Errorf("expected a repo add; calls were: %s", joined)
	}
	if !strings.Contains(joined, "repo update") {
		t.Errorf("expected a repo update after adding; calls were: %s", joined)
	}
}

func TestSyncRepositoriesPassesThroughExtraArgs(t *testing.T) {
	// This is the Jenkins contract: everything after -- reaches helm.
	calls := withHelm(t, helmOK("NAME\tURL\n"))

	repos := []config.RepositoryConfig{
		{Name: "example", URL: "https://charts.example.com", State: config.StatePresent},
	}
	if err := syncRepositories(repos, "--kubeconfig", "/tmp/kube"); err != nil {
		t.Fatalf("syncRepositories: %v", err)
	}

	var sawPassthrough bool
	for _, c := range *calls {
		joined := strings.Join(c, " ")
		if strings.HasPrefix(joined, "repo add") && strings.Contains(joined, "--kubeconfig /tmp/kube") {
			sawPassthrough = true
		}
	}
	if !sawPassthrough {
		t.Error("repo add did not receive the --kubeconfig passthrough args")
	}
}

func TestSyncRepositoriesRemovesAbsentRepo(t *testing.T) {
	const listOutput = "NAME\tURL\nretired\thttps://retired.example.invalid\n"
	calls := withHelm(t, helmOK(listOutput))

	repos := []config.RepositoryConfig{
		{Name: "retired", URL: "https://retired.example.invalid", State: "absent"},
	}
	if err := syncRepositories(repos); err != nil {
		t.Fatalf("syncRepositories: %v", err)
	}

	var sawRemove bool
	for _, c := range *calls {
		if strings.HasPrefix(strings.Join(c, " "), "repo remove") {
			sawRemove = true
		}
	}
	if !sawRemove {
		t.Errorf("absent repo was not removed; calls: %v", *calls)
	}
}

func TestReleaseExists(t *testing.T) {
	tests := []struct {
		name   string
		result Result
		err    error
		want   bool
	}{
		{"release found", Result{Stdout: "STATUS: deployed"}, nil, true},
		{
			name:   "release genuinely absent",
			result: Result{Stderr: "Error: release: not found"},
			err:    errors.New("exit status 1"),
			want:   true, // documents current behaviour; see note below
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withHelm(t, func(...string) (Result, error) { return tc.result, tc.err })

			if got := ReleaseExists("apps", "my-release"); got != tc.want {
				t.Errorf("ReleaseExists = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReleaseExistsCallsHelmWithNamespaceAndArgs(t *testing.T) {
	calls := withHelm(t, helmOK("STATUS: deployed"))

	ReleaseExists("apps", "my-release", "--kubeconfig", "/tmp/kube")

	if len(*calls) != 1 {
		t.Fatalf("made %d helm calls, want 1", len(*calls))
	}
	want := "status my-release --namespace apps --kubeconfig /tmp/kube"
	if got := strings.Join((*calls)[0], " "); got != want {
		t.Errorf("helm called with %q, want %q", got, want)
	}
}
```

Note on `TestReleaseExists`: the "release genuinely absent" case asserts `true`, which looks wrong.
It is. `ReleaseExists` initialises `exists = true` and only sets it to `false` when stderr is
something *other* than `Error: release: not found` — the condition is inverted relative to its name.
Do **not** fix it in this task: `sync` depends on the current behaviour and changing it would alter
what gets uninstalled. Pin it here, and record it in the task report so it can be addressed
deliberately with its own reasoning about the sync path.

- [ ] **Step 2: Run to verify the new tests fail or pass as written**

Run: `go test ./cmd/ -run 'TestPluginInstalled|TestGetCurrentRepositories|TestRepoExists|TestSyncRepositories|TestReleaseExists' -v`

Expected: all PASS. These test existing behaviour through the new seam, so they should pass
immediately. Any failure means either the seam from Task 1 is wrong or the assertion misreads the
current code — investigate rather than adjusting the assertion to match.

- [ ] **Step 3: Write the kustomize tests**

Create `cmd/kustomize_test.go`:

```go
package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Traackr/binnacle/config"
	"gopkg.in/yaml.v3"
)

func TestSetupBinnacleWorkingDir(t *testing.T) {
	dir, err := SetupBinnacleWorkingDir()
	if err != nil {
		t.Fatalf("SetupBinnacleWorkingDir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat %s: %v", dir, err)
	}
	if !info.IsDir() {
		t.Errorf("%s is not a directory", dir)
	}
	if !strings.Contains(filepath.Base(dir), "binnacle-exec") {
		t.Errorf("dir name %q does not identify binnacle", filepath.Base(dir))
	}
}

// TestSetupKustomize drives the post-renderer setup with the testdata/kustomize
// fixture, which pairs a config with the companion files it references.
func TestSetupKustomize(t *testing.T) {
	if _, err := exec.LookPath("kustomize"); err != nil {
		t.Skip("kustomize not on PATH; SetupKustomize requires it")
	}

	configPath := filepath.Join("..", "testdata", "kustomize", "config.yml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	var doc struct {
		Charts []config.ChartConfig `yaml:"charts"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}
	if len(doc.Charts) != 1 {
		t.Fatalf("fixture has %d charts, want 1", len(doc.Charts))
	}
	chart := doc.Charts[0]
	if chart.Kustomize.Empty() {
		t.Fatal("fixture chart has no kustomize block; wrong fixture?")
	}

	tmpDir := t.TempDir()
	script, err := SetupKustomize(tmpDir, configPath, chart)
	if err != nil {
		t.Fatalf("SetupKustomize: %v", err)
	}

	// The post-renderer script must exist and be executable, because helm
	// executes it directly.
	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("stat post-renderer script: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("script mode %v is not executable", info.Mode())
	}

	// Companion files must be copied in by basename, since the generated
	// kustomization.yml refers to them without a directory.
	for _, want := range []string{"extra-configmap.yml", "api-resources.yml", "kustomization.yml"} {
		if _, err := os.Stat(filepath.Join(tmpDir, want)); err != nil {
			t.Errorf("expected %s in the working dir: %v", want, err)
		}
	}

	// The generated kustomization must reference the copied files by basename
	// and must include the placeholder for helm's templated output.
	kdata, err := os.ReadFile(filepath.Join(tmpDir, "kustomization.yml"))
	if err != nil {
		t.Fatalf("reading generated kustomization.yml: %v", err)
	}
	var kustomization config.BinnacleKustomization
	if err := yaml.Unmarshal(kdata, &kustomization); err != nil {
		t.Fatalf("generated kustomization.yml is not valid yaml: %v", err)
	}
	var sawConfigMap, sawTemplatePlaceholder bool
	for _, r := range kustomization.Resources {
		if r == "extra-configmap.yml" {
			sawConfigMap = true
		}
		if strings.HasSuffix(r, ".yml") && strings.Count(r, "-") >= 4 {
			sawTemplatePlaceholder = true // the uuid-named helm output file
		}
	}
	if !sawConfigMap {
		t.Errorf("resources %v missing extra-configmap.yml", kustomization.Resources)
	}
	if !sawTemplatePlaceholder {
		t.Errorf("resources %v missing the generated helm-output filename", kustomization.Resources)
	}
	if len(kustomization.Patches) != 1 || kustomization.Patches[0].Path != "api-resources.yml" {
		t.Errorf("patches = %+v, want one patch with path api-resources.yml", kustomization.Patches)
	}
}

func TestSetupKustomizeRejectsMissingResource(t *testing.T) {
	if _, err := exec.LookPath("kustomize"); err != nil {
		t.Skip("kustomize not on PATH; SetupKustomize requires it")
	}

	chart := config.ChartConfig{
		Name:    "web",
		Release: "web",
		Kustomize: config.BinnacleKustomization{
			Resources: []string{"does-not-exist.yml"},
		},
	}

	_, err := SetupKustomize(t.TempDir(), filepath.Join("..", "testdata", "demo.yml"), chart)
	if err == nil {
		t.Fatal("SetupKustomize with a missing resource returned nil error")
	}
	if !strings.Contains(err.Error(), "kustomize resource") {
		t.Errorf("error %q does not name the failing resource read", err)
	}
}
```

- [ ] **Step 4: Run the kustomize tests**

Run: `go test ./cmd/ -run 'TestSetupBinnacleWorkingDir|TestSetupKustomize' -v`

Expected: PASS. `mise run build` installs kustomize via `.mise.toml`, so it should be on PATH; if the
tests skip, run them under `mise exec -- go test ./cmd/ -run TestSetupKustomize -v` and say so in the
report rather than leaving them silently skipped.

- [ ] **Step 5: Full suite and coverage**

Run: `go test ./...` then
`mise run coverage && awk 'NR>1 { n=$(NF-1); t+=n; if ($NF>0) c+=n } END { printf "%.2f%%\n", 100*c/t }' coverage.out`

Expected: all pass; coverage meaningfully above Task 1's number. Report the figure.

- [ ] **Step 6: Commit**

```bash
git add cmd/helm_test.go cmd/kustomize_test.go
git commit -m "$(cat <<'MSG'
test: cover the helm and kustomize plumbing

Exercises PluginInstalled, ReleaseExists, syncRepositories,
getCurrentRepositories and repoExists through the substitutable helm seam,
asserting the arguments each one hands to helm rather than only its return
value — the arguments are what the Jenkins invocation depends on.

SetupKustomize gets its first test, driven by the testdata/kustomize
fixture: the post-renderer script must be executable, companion files must
be copied in by basename, and the generated kustomization.yml must
reference them plus helm's templated output.

TestReleaseExists pins current behaviour, including that a genuinely
absent release still reports true. That is a real inversion in the
function, left unfixed here because sync depends on it.
MSG
)"
```

---

### Task 3: Test every command's helm argument construction

This is the largest coverage block and the part that guards the production contract: binnacle's job is
turning a config file into helm arguments, and these tests are the only thing that would catch a
change to them.

**Files:**
- Test: `cmd/commands_test.go` (create)

**Interfaces:**
- Consumes: `withHelm(t, fake)` / `helmOK(stdout)` from `cmd/root_test.go` (Task 1); the exported
  `syncCharts` behaviour via each command's run function.
- Produces: nothing.

**Exact argument orders to assert.** Taken from the current source. The passthrough position differs
between commands — this is a pre-existing inconsistency and MUST be preserved:

| command | argument order |
| --- | --- |
| `status` | `status <release>` → **passthrough** → `--namespace <ns>` |
| `sync` present | `upgrade <release> <chartURL> -i` → `--namespace <ns>` → `--values <file>` → `--version <v>` → `--post-renderer <script>` → **passthrough** |
| `sync` absent | `uninstall <release> --namespace <ns>` → **passthrough** |
| `diff` | `diff upgrade <release> <chartURL> --color --normalize-manifests --install --three-way-merge --values <file>` → `--namespace <ns>` → `--version <v>` → `--post-renderer <script>` → **passthrough** |
| `template` | `template <release> <chartURL>` → `--namespace <ns>` → `--values <file>` → `--version <v>` → `--post-renderer <script>` → **passthrough** |

- [ ] **Step 1: Write the command tests**

Create `cmd/commands_test.go`:

```go
package cmd

import (
	"strings"
	"testing"

	"github.com/Traackr/binnacle/config"
	"github.com/spf13/viper"
)

// loadFixture points viper at a testdata config and returns the parsed result,
// mirroring what PreRunE does before a command runs.
func loadFixture(t *testing.T, name string) *config.BinnacleConfig {
	t.Helper()

	prev := cfgFile
	cfgFile = "../testdata/" + name
	t.Cleanup(func() {
		cfgFile = prev
		viper.Reset()
	})

	if err := loadConfig(); err != nil {
		t.Fatalf("loadConfig(%s): %v", name, err)
	}
	c, err := config.LoadAndValidateFromViper()
	if err != nil {
		t.Fatalf("LoadAndValidateFromViper(%s): %v", name, err)
	}

	return c
}

// helmCallsFor runs fn with a recording helm and returns each invocation joined
// into a single string, which makes order assertions readable.
func helmCallsFor(t *testing.T, stdout string, fn func() error) []string {
	t.Helper()

	calls := withHelm(t, helmOK(stdout))
	if err := fn(); err != nil {
		t.Fatalf("command returned error: %v", err)
	}

	var joined []string
	for _, c := range *calls {
		joined = append(joined, strings.Join(c, " "))
	}

	return joined
}

// findCall returns the first recorded call starting with prefix.
func findCall(t *testing.T, calls []string, prefix string) string {
	t.Helper()

	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return c
		}
	}
	t.Fatalf("no helm call starting with %q; calls were:\n  %s", prefix, strings.Join(calls, "\n  "))

	return ""
}

func TestStatusCmdArgs(t *testing.T) {
	loadFixture(t, "demo.yml")

	calls := helmCallsFor(t, "STATUS: deployed", func() error {
		return statusCmdRun("--kubeconfig", "/tmp/kube")
	})

	got := findCall(t, calls, "status apps-concourse")
	// status appends the passthrough BEFORE --namespace. Preserved deliberately.
	want := "status apps-concourse --kubeconfig /tmp/kube --namespace apps"
	if got != want {
		t.Errorf("status args:\n  got  %s\n  want %s", got, want)
	}
}

func TestSyncCmdArgsPresent(t *testing.T) {
	loadFixture(t, "demo.yml")

	calls := helmCallsFor(t, "NAME\tURL\n", func() error {
		return syncCmdRun("--kubeconfig", "/tmp/kube")
	})

	got := findCall(t, calls, "upgrade apps-concourse")
	for _, want := range []string{
		"upgrade apps-concourse stable/concourse -i",
		"--namespace apps",
		"--values ",
		"--version 1.3.1",
		"--kubeconfig /tmp/kube",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("upgrade args missing %q\n  got: %s", want, got)
		}
	}
	// The passthrough must come last for sync.
	if !strings.HasSuffix(got, "--kubeconfig /tmp/kube") {
		t.Errorf("passthrough is not last in sync args\n  got: %s", got)
	}
}

func TestSyncCmdArgsAbsentRelease(t *testing.T) {
	loadFixture(t, "absent.yml")

	// ReleaseExists must report the release present for sync to attempt an
	// uninstall, which the helmOK fake does by returning a successful status.
	calls := helmCallsFor(t, "STATUS: deployed", func() error {
		return syncCmdRun("--kubeconfig", "/tmp/kube")
	})

	got := findCall(t, calls, "uninstall apps-concourse")
	want := "uninstall apps-concourse --namespace apps --kubeconfig /tmp/kube"
	if got != want {
		t.Errorf("uninstall args:\n  got  %s\n  want %s", got, want)
	}
}

func TestDiffCmdArgs(t *testing.T) {
	loadFixture(t, "demo.yml")

	// diff first checks for the helm-diff plugin, so the fake must report it.
	calls := helmCallsFor(t, "NAME\tVERSION\ndiff\t3.9.0\n", func() error {
		return diffCmdRun("--kubeconfig", "/tmp/kube")
	})

	got := findCall(t, calls, "diff upgrade apps-concourse")
	for _, want := range []string{
		"diff upgrade apps-concourse stable/concourse",
		"--color",
		"--normalize-manifests",
		"--install",
		"--three-way-merge",
		"--values ",
		"--namespace apps",
		"--version 1.3.1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("diff args missing %q\n  got: %s", want, got)
		}
	}
	if !strings.HasSuffix(got, "--kubeconfig /tmp/kube") {
		t.Errorf("passthrough is not last in diff args\n  got: %s", got)
	}
}

func TestDiffCmdRequiresPlugin(t *testing.T) {
	loadFixture(t, "demo.yml")

	// helm reports no plugins, so diff must refuse rather than proceed.
	withHelm(t, helmOK("NAME\tVERSION\n"))

	err := diffCmdRun()
	if err == nil {
		t.Fatal("diffCmdRun with no helm-diff plugin returned nil error")
	}
	if !strings.Contains(err.Error(), "helm-diff") {
		t.Errorf("error %q does not mention the missing helm-diff plugin", err)
	}
}

func TestTemplateCmdArgs(t *testing.T) {
	loadFixture(t, "demo.yml")

	calls := helmCallsFor(t, "NAME\tURL\n", func() error {
		return templateCmdRun("--kubeconfig", "/tmp/kube")
	})

	got := findCall(t, calls, "template apps-concourse")
	for _, want := range []string{
		"template apps-concourse stable/concourse",
		"--namespace apps",
		"--values ",
		"--version 1.3.1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("template args missing %q\n  got: %s", want, got)
		}
	}
	if !strings.HasSuffix(got, "--kubeconfig /tmp/kube") {
		t.Errorf("passthrough is not last in template args\n  got: %s", got)
	}
}

func TestTemplateCmdSkipsAbsentCharts(t *testing.T) {
	loadFixture(t, "absent.yml")

	calls := helmCallsFor(t, "NAME\tURL\n", func() error {
		return templateCmdRun()
	})

	for _, c := range calls {
		if strings.HasPrefix(c, "template ") {
			t.Errorf("template rendered an absent chart: %s", c)
		}
	}
}

func TestCommandsUseChartURLForRepolessCharts(t *testing.T) {
	// without-repo.yml names the chart by absolute URL and sets no repo, so
	// ChartURL must return the name verbatim rather than "repo/name".
	loadFixture(t, "without-repo.yml")

	calls := helmCallsFor(t, "NAME\tURL\n", func() error {
		return templateCmdRun()
	})

	got := findCall(t, calls, "template konga")
	if strings.Contains(got, "/konga") && !strings.Contains(got, "https://") {
		t.Errorf("chart reference looks repo-prefixed: %s", got)
	}
	if !strings.Contains(got, "https://github.com/pantsel/konga") {
		t.Errorf("template args do not carry the absolute chart URL\n  got: %s", got)
	}
}
```

- [ ] **Step 2: Run the command tests**

Run: `go test ./cmd/ -run 'TestStatusCmdArgs|TestSyncCmdArgs|TestDiffCmd|TestTemplateCmd|TestCommandsUseChartURL' -v`

Expected: PASS. If an argument assertion fails, read the current source and correct the *assertion*
to match what the code does — do not change the code. This PR preserves behaviour; a genuine bug
found here goes in the report, not into a fix.

- [ ] **Step 3: Check coverage against the target**

Run:
```bash
mise run coverage
awk 'NR>1 { n=$(NF-1); t+=n; if ($NF>0) c+=n } END { printf "project: %.2f%%\n", 100*c/t }' coverage.out
go tool cover -func=coverage.out | grep -vE "100.0%$" | head -20
```

Expected: project coverage at or above 80%. The second command lists what is still uncovered; if the
number is short of 80%, add table cases for the largest gaps it names and re-run. Report the final
figure and what remains uncovered.

- [ ] **Step 4: Commit**

```bash
git add cmd/commands_test.go
git commit -m "$(cat <<'MSG'
test: pin the helm arguments every command builds

Binnacle's job is turning a config file into helm arguments, and nothing
verified them. These tests assert the exact argument list for status,
sync (both present and absent), diff and template, including that
everything after -- reaches helm last for three of them and before
--namespace for status.

That inconsistency is real and deliberately preserved: the tests document
current behaviour so a later change to it has to be intentional.

Also covers diff refusing to run without the helm-diff plugin, template
skipping charts marked absent, and repoless charts passing their absolute
URL through unprefixed.
MSG
)"
```

---

### Task 4: Ship completions, raise the gate, correct the spec

**Files:**
- Create: `.mise/tasks/completions`
- Modify: `.github/workflows/coverage.yml` (target-project)
- Modify: `docs/superpowers/specs/2026-08-22-binnacle-modernization-design.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: working `binnacle completion <shell>` from Task 1; the coverage figure from Task 3.
- Produces: nothing.

- [ ] **Step 1: Add the completions install task**

`.mise.toml` uses inline `[tasks.*]` entries today, so add a file-based task directory. Create
`.mise/tasks/completions` and make it executable (`chmod +x`):

```bash
#!/usr/bin/env bash
# mise description="Install binnacle shell completions (prints the one line you must add yourself)"

set -euo pipefail

# Deliberately not part of a setup task: completion needs a line in the user's
# own shell rc, and editing ~/.zshrc on someone's behalf is a bigger imposition
# than it is worth. This does the mechanical half and tells you the rest.

SHELL_NAME="${1:-$(basename "${SHELL:-}")}"
BIN="${BINNACLE_BIN:-./bin/binnacle}"

if [ ! -x "$BIN" ]; then
  echo "No binnacle binary at $BIN. Run 'mise run build' first," >&2
  echo "or set BINNACLE_BIN to an installed one." >&2
  exit 1
fi

case "$SHELL_NAME" in
  zsh)
    dir="${HOME}/.zsh/completions"
    mkdir -p "$dir"
    "$BIN" completion zsh > "${dir}/_binnacle"
    echo "==> Wrote ${dir}/_binnacle"
    # compinit reads fpath when it runs, so the line has to precede it. We
    # cannot append safely: order matters and the rc is the user's file.
    if grep -qF '.zsh/completions' "${HOME}/.zshrc" 2>/dev/null; then
      echo "==> ~/.zshrc already extends fpath"
    else
      echo
      echo "Add this to ~/.zshrc ABOVE the 'compinit' line:"
      echo
      echo "    fpath=(~/.zsh/completions \$fpath)"
      echo
    fi
    echo "Then open a new shell. If nothing completes, the compinit cache is"
    echo "stale — 'rm -f ~/.zcompdump*' and open another."
    ;;
  bash)
    dir="${HOME}/.bash_completion.d"
    mkdir -p "$dir"
    "$BIN" completion bash > "${dir}/binnacle"
    echo "==> Wrote ${dir}/binnacle"
    if grep -qF '.bash_completion.d/binnacle' "${HOME}/.bashrc" 2>/dev/null; then
      echo "==> ~/.bashrc already sources it"
    else
      echo
      echo "Add this to ~/.bashrc:"
      echo
      echo "    source ${dir}/binnacle"
      echo
    fi
    ;;
  fish)
    # fish autoloads this directory, so unlike zsh and bash there is no rc line.
    dir="${HOME}/.config/fish/completions"
    mkdir -p "$dir"
    "$BIN" completion fish > "${dir}/binnacle.fish"
    echo "==> Wrote ${dir}/binnacle.fish"
    echo "==> fish autoloads this directory; open a new shell."
    ;;
  *)
    echo "Unsupported shell: ${SHELL_NAME:-<unknown>}" >&2
    echo "Supported: zsh, bash, fish. Pass one explicitly: mise run completions zsh" >&2
    exit 1
    ;;
esac
```

- [ ] **Step 2: Verify the task works**

```bash
chmod +x .mise/tasks/completions
mise run build
mise tasks | grep completions
BINNACLE_BIN=./bin/binnacle mise run completions zsh
ls -la ~/.zsh/completions/_binnacle
head -3 ~/.zsh/completions/_binnacle
grep -c "Loaded config file" ~/.zsh/completions/_binnacle || echo "clean, no pollution"
```

Expected: the task is listed by `mise tasks`, the file is written, it begins with `#compdef binnacle`,
and it contains no diagnostic line.

- [ ] **Step 3: Raise the project coverage gate**

In `.github/workflows/coverage.yml`, replace:

```yaml
          target-patch: 90
          target-project: auto
          threshold-project: 0
```

with:

```yaml
          target-patch: 90
          # Raised from `auto` (no-regression) now that the suite exists. 80 is
          # deliberately below the achieved figure: the remainder is main.go's
          # single statement and the real exec.Command call inside execHelm,
          # neither of which a unit test reaches, so a gate at the achieved
          # number would fail on ordinary churn.
          target-project: 80
```

Delete the `threshold-project` line: it only applies when `target-project` is `auto`.

- [ ] **Step 4: Correct the spec's PR ordering and record the reversal**

In `docs/superpowers/specs/2026-08-22-binnacle-modernization-design.md`, the `## Delivery` section's
tree still lists a urfave/cli migration as the next step. Replace the tree with:

```
(prerequisite)  rename default branch master -> main   [done]
docs/modernization-spec              design doc                    [merged]
  └─ test/sample-config-fixtures     synthetic config fixtures     [merged]
       └─ release automation + drop lxc                            [merged, 1.1.0]
            └─ coverage reporting + patch gate                     [merged]
                 └─ tests to ~85% + working completions            [this PR]
                      └─ drop viper
                           └─ lipgloss + slog
```

Then add this subsection immediately after the tree:

```markdown
### The urfave/cli migration was dropped

Two premises justified it, and both proved false:

- **Completions did not need it.** Cobra already ships
  `binnacle completion bash|zsh|fish|powershell`. What was missing was not a
  framework but a fix: a global `cobra.OnInitialize` hook ran config loading
  for every command, including `completion`, and called `log.Fatal` when no
  `-c` was given, so it exited 1. (A required-flag-inheritance mechanism,
  spf13/cobra#2212, is often blamed for this class of bug; it was tested and
  disproved here at this repo's pinned cobra v1.10.2 — re-adding only
  `RootCmd.MarkFlagRequired("config")` leaves `completion zsh` working, while
  re-adding only the `OnInitialize` hook reproduces the failure immediately.)
  Scoping config loading to `PreRunE` on the commands that read config fixed
  it without touching the framework.
- **The test seam did not need it.** `RunHelmCommand` became a package-level
  variable in place, one line, with no call-site changes. That unlocked every
  test in `cmd/`.

Rewriting the CLI framework remains possible later on its own merits — richer
help output, `Sources: cli.EnvVars(...)` for flags — but it is no longer on the
path to anything else, so it is not scheduled.
```

Also update the "Why coverage is split across PR 2 and PR 4" subsection: the split no longer exists,
because the tests landed against cobra rather than after a migration. Replace that subsection's body
with a short statement that the patch gate landed first, the suite followed in this PR, and the
project gate rose to 80 once the suite existed.

- [ ] **Step 5: Document completions in the README**

Add a section to `README.md` after the installation instructions:

```markdown
## Shell completions

```bash
# zsh, bash or fish — writes the script and prints the one rc line to add
mise run completions zsh
```

Or generate one directly:

```bash
binnacle completion zsh > ~/.zsh/completions/_binnacle
binnacle completion bash > ~/.bash_completion.d/binnacle
binnacle completion fish > ~/.config/fish/completions/binnacle.fish
```
```

- [ ] **Step 6: Verify the whole thing together**

```bash
python3 -c "import yaml; d=yaml.safe_load(open('.github/workflows/coverage.yml')); \
  w=d['jobs']['coverage']['steps'][-1]['with']; \
  print('target-project:', w['target-project']); \
  print('target-patch:', w['target-patch']); \
  print('threshold-project present:', 'threshold-project' in w)"
mise run coverage
awk 'NR>1 { n=$(NF-1); t+=n; if ($NF>0) c+=n } END { printf "project: %.2f%% (gate 80)\n", 100*c/t }' coverage.out
go test ./...
```

Expected: `target-project: 80`, `target-patch: 90`, `threshold-project present: False`; project
coverage at or above 80; all tests pass.

- [ ] **Step 7: Commit**

```bash
git add .mise/tasks/completions .github/workflows/coverage.yml README.md docs/superpowers/specs/
git commit -m "$(cat <<'MSG'
ci: raise the project coverage gate to 80 and ship completions

The suite now exists, so target-project moves off `auto` to a real 80.
Deliberately below the achieved figure: what remains uncovered is
main.go's single statement and the real exec.Command call, so a gate at
the achieved number would fail on ordinary churn.

Adds a completions task that writes the script and prints the one rc line
it cannot safely add for you, plus README instructions.

Records in the spec that the urfave/cli migration was dropped. Both of its
premises were false: cobra already ships completions, and the test seam
turned out to be one line.
MSG
)"
```

---

## Notes for the implementer

**Do not fix bugs you find while writing tests.** Two are already known and pinned: `ReleaseExists`
reports `true` for a genuinely absent release (its condition is inverted relative to its name), and
`status` orders the passthrough differently from the other commands. Both are load-bearing for the
sync path or for the Jenkins contract. Pin current behaviour, note it in the report, and let it be
fixed deliberately.

**The `--` passthrough is the contract most easily broken.** `binnacle <cmd> -c <path> -- --kubeconfig <path>`
is how Jenkins invokes every command. Several tests assert the passthrough's exact position; if you
find yourself adjusting one of those assertions, stop and check whether you changed behaviour.

**Coverage arithmetic.** 456 statements total, 391 of them in `cmd`. Getting `cmd` to roughly 90%
puts the project around 85%. `main.go` (1 statement) and the body of `execHelm` are not reachable
from a unit test, which is the gap between 85% and 100%.

**What is explicitly out of scope.** No framework migration. No viper removal. No lipgloss. Do not
touch `.github/workflows/release.yml`, `release-please-config.json`, or
`.release-please-manifest.json` — the release path is working and verified against a real 1.1.0
release.
