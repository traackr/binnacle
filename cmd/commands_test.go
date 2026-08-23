package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/Traackr/binnacle/config"
)

// loadFixture points viper at a testdata config and returns the parsed result,
// mirroring what PreRunE does before a command runs.
func loadFixture(t *testing.T, name string) *config.BinnacleConfig {
	t.Helper()

	prev := cfgFile
	cfgFile = "../testdata/" + name
	t.Cleanup(func() {
		cfgFile = prev
		resetViperKeepingFlags()
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

// loadConfigOnly points viper at a testdata config, like loadFixture, but
// stops after loadConfig() and skips LoadAndValidateFromViper. Fixtures used
// here are deliberately invalid at that later stage - the assertion under
// test is that a command's own call to LoadAndValidateFromViper propagates
// the error, not that the fixture loads cleanly.
func loadConfigOnly(t *testing.T, name string) {
	t.Helper()

	prev := cfgFile
	cfgFile = "../testdata/" + name
	t.Cleanup(func() {
		cfgFile = prev
		resetViperKeepingFlags()
	})

	if err := loadConfig(); err != nil {
		t.Fatalf("loadConfig(%s): %v", name, err)
	}
}

// helmFailOn returns a fake helm executor that fails for any call whose
// joined args start with prefix, and otherwise succeeds with okStdout. It
// lets a test target one specific helm invocation in a multi-call command
// (e.g. "repo add" or "upgrade") without disturbing the calls around it.
func helmFailOn(prefix, okStdout string) func(args ...string) (Result, error) {
	return func(args ...string) (Result, error) {
		if strings.HasPrefix(strings.Join(args, " "), prefix) {
			return Result{Stderr: "boom"}, errors.New("boom")
		}
		return Result{Stdout: okStdout}, nil
	}
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

// --- Cobra wiring ---
//
// Everything above calls each command's Run function directly. These four
// exercise the cobra.Command's own PreRunE/RunE/PostRun fields, which is the
// path binnacle actually runs through - loadConfig, the *CmdPreRun logger,
// the Run function, and the *CmdPostRun logger all wired together.

func TestStatusCmdWiring(t *testing.T) {
	loadFixture(t, "demo.yml")
	calls := withHelm(t, helmOK("STATUS: deployed"))

	if err := statusCmd.PreRunE(statusCmd, nil); err != nil {
		t.Fatalf("statusCmd.PreRunE: %v", err)
	}
	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("statusCmd.RunE: %v", err)
	}
	statusCmd.PostRun(statusCmd, nil)

	if len(*calls) == 0 {
		t.Fatal("statusCmd.RunE made no helm calls")
	}
}

func TestSyncCmdWiring(t *testing.T) {
	loadFixture(t, "demo.yml")
	calls := withHelm(t, helmOK("NAME\tURL\n"))

	if err := syncCmd.PreRunE(syncCmd, nil); err != nil {
		t.Fatalf("syncCmd.PreRunE: %v", err)
	}
	if err := syncCmd.RunE(syncCmd, nil); err != nil {
		t.Fatalf("syncCmd.RunE: %v", err)
	}
	syncCmd.PostRun(syncCmd, nil)

	if len(*calls) == 0 {
		t.Fatal("syncCmd.RunE made no helm calls")
	}
}

func TestDiffCmdWiring(t *testing.T) {
	loadFixture(t, "demo.yml")
	calls := withHelm(t, helmOK("NAME\tVERSION\ndiff\t3.9.0\n"))

	if err := diffCmd.PreRunE(diffCmd, nil); err != nil {
		t.Fatalf("diffCmd.PreRunE: %v", err)
	}
	if err := diffCmd.RunE(diffCmd, nil); err != nil {
		t.Fatalf("diffCmd.RunE: %v", err)
	}
	diffCmd.PostRun(diffCmd, nil)

	if len(*calls) == 0 {
		t.Fatal("diffCmd.RunE made no helm calls")
	}
}

func TestTemplateCmdWiring(t *testing.T) {
	loadFixture(t, "demo.yml")
	calls := withHelm(t, helmOK("NAME\tURL\n"))

	if err := templateCmd.PreRunE(templateCmd, nil); err != nil {
		t.Fatalf("templateCmd.PreRunE: %v", err)
	}
	if err := templateCmd.RunE(templateCmd, nil); err != nil {
		t.Fatalf("templateCmd.RunE: %v", err)
	}
	templateCmd.PostRun(templateCmd, nil)

	if len(*calls) == 0 {
		t.Fatal("templateCmd.RunE made no helm calls")
	}
}

// --- Config-load error propagation ---
//
// Each command loads its own config via LoadAndValidateFromViper; these pin
// that a validation failure there is returned rather than swallowed.
// unmarshallable.yml parses as YAML (so loadConfig succeeds) but fails
// viper's strict UnmarshalExact decode (so LoadAndValidateFromViper doesn't).

func TestStatusCmdRunPropagatesConfigError(t *testing.T) {
	loadConfigOnly(t, "unmarshallable.yml")

	if err := statusCmdRun(); err == nil {
		t.Fatal("statusCmdRun with an invalid config returned nil, want an error")
	}
}

func TestSyncCmdRunPropagatesConfigError(t *testing.T) {
	loadConfigOnly(t, "unmarshallable.yml")

	if err := syncCmdRun(); err == nil {
		t.Fatal("syncCmdRun with an invalid config returned nil, want an error")
	}
}

func TestDiffCmdRunPropagatesConfigError(t *testing.T) {
	// diff checks for the helm-diff plugin before it loads config, so that
	// check must succeed for the config error to be the one that surfaces.
	withHelm(t, helmOK("NAME\tVERSION\ndiff\t3.9.0\n"))
	loadConfigOnly(t, "unmarshallable.yml")

	if err := diffCmdRun(); err == nil {
		t.Fatal("diffCmdRun with an invalid config returned nil, want an error")
	}
}

func TestTemplateCmdRunPropagatesConfigError(t *testing.T) {
	loadConfigOnly(t, "unmarshallable.yml")

	if err := templateCmdRun(); err == nil {
		t.Fatal("templateCmdRun with an invalid config returned nil, want an error")
	}
}

// --- Helm call error propagation ---
//
// Each command wraps a failing helm invocation in its own error rather than
// swallowing it. These target one specific call in an otherwise-successful
// sequence via helmFailOn.

func TestStatusCmdRunPropagatesHelmError(t *testing.T) {
	loadFixture(t, "demo.yml")
	withHelm(t, helmFailOn("status", ""))

	err := statusCmdRun()
	if err == nil {
		t.Fatal("statusCmdRun with a failing helm status returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "running helm status for release") {
		t.Errorf("error %q does not mention the failing helm status call", err)
	}
}

func TestSyncCmdRunPropagatesChartError(t *testing.T) {
	loadFixture(t, "demo.yml")
	withHelm(t, helmFailOn("upgrade", "NAME\tURL\n"))

	err := syncCmdRun()
	if err == nil {
		t.Fatal("syncCmdRun with a failing helm upgrade returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "running helm sync for release") {
		t.Errorf("error %q does not mention the failing helm upgrade call", err)
	}
}

func TestSyncCmdRunPropagatesRepositoryError(t *testing.T) {
	loadFixture(t, "demo.yml")
	withHelm(t, helmFailOn("repo add", "NAME\tURL\n"))

	err := syncCmdRun()
	if err == nil {
		t.Fatal("syncCmdRun with a failing helm repo add returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "running helm repo add") {
		t.Errorf("error %q does not mention the failing helm repo add call", err)
	}
}

func TestDiffCmdRunPropagatesPluginCheckError(t *testing.T) {
	loadFixture(t, "demo.yml")
	// The "plugin list" call itself errors - distinct from TestDiffCmdRequiresPlugin,
	// where it succeeds but doesn't list "diff".
	withHelm(t, helmFailOn("plugin", ""))

	err := diffCmdRun()
	if err == nil {
		t.Fatal("diffCmdRun with a failing helm plugin list returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "detecting if helm-diff plugin is installed") {
		t.Errorf("error %q does not mention the plugin detection failure", err)
	}
}

func TestDiffCmdRunPropagatesChartError(t *testing.T) {
	loadFixture(t, "demo.yml")
	withHelm(t, helmFailOn("diff upgrade", "NAME\tVERSION\ndiff\t3.9.0\n"))

	err := diffCmdRun()
	if err == nil {
		t.Fatal("diffCmdRun with a failing helm diff upgrade returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "running helm diff for release") {
		t.Errorf("error %q does not mention the failing helm diff call", err)
	}
}

func TestDiffCmdRunPropagatesRepositoryError(t *testing.T) {
	loadFixture(t, "demo.yml")
	withHelm(t, helmFailOn("repo add", "NAME\tVERSION\ndiff\t3.9.0\n"))

	err := diffCmdRun()
	if err == nil {
		t.Fatal("diffCmdRun with a failing helm repo add returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "running helm repo add") {
		t.Errorf("error %q does not mention the failing helm repo add call", err)
	}
}

func TestTemplateCmdRunPropagatesChartError(t *testing.T) {
	loadFixture(t, "demo.yml")
	withHelm(t, helmFailOn("template", "NAME\tURL\n"))

	err := templateCmdRun()
	if err == nil {
		t.Fatal("templateCmdRun with a failing helm template returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "running helm template for release") {
		t.Errorf("error %q does not mention the failing helm template call", err)
	}
}

func TestTemplateCmdRunPropagatesRepositoryError(t *testing.T) {
	loadFixture(t, "demo.yml")
	withHelm(t, helmFailOn("repo add", "NAME\tURL\n"))

	err := templateCmdRun()
	if err == nil {
		t.Fatal("templateCmdRun with a failing helm repo add returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "running helm repo add") {
		t.Errorf("error %q does not mention the failing helm repo add call", err)
	}
}

// --- loadConfig error branches not reached by root_test.go ---
//
// TestLoadConfigReturnsErrorWithoutConfigFile (root_test.go) covers the
// "no -c given" early return. These two cover the remaining branches: the
// config file existing but not being readable, and the loglevel value in it
// (or environment) not parsing as a logrus level.

func TestLoadConfigPropagatesReadError(t *testing.T) {
	prev := cfgFile
	cfgFile = "../testdata/does-not-exist.yml"
	t.Cleanup(func() {
		cfgFile = prev
		resetViperKeepingFlags()
	})

	if err := loadConfig(); err == nil {
		t.Fatal("loadConfig with a missing config file returned nil, want an error")
	}
}

func TestLoadConfigPropagatesLogLevelError(t *testing.T) {
	t.Setenv("LOGLEVEL", "not-a-real-level")
	prev := cfgFile
	cfgFile = "../testdata/demo.yml"
	t.Cleanup(func() {
		cfgFile = prev
		resetViperKeepingFlags()
	})

	if err := loadConfig(); err == nil {
		t.Fatal("loadConfig with an invalid loglevel returned nil, want an error")
	}
}
