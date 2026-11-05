package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The tests in this file run binnacle against the real helm and kustomize on
// PATH. What they cover is helm's contract rather than binnacle's logic: what
// --post-renderer accepts, and which plugins stay visible once binnacle has
// pointed HELM_PLUGINS somewhere else. A fake helm cannot notice either of those
// changing, which is how kustomize support broke on Helm 4 unnoticed.

// requireRealTools skips without helm and kustomize, and makes sure nothing in
// the test can reach a cluster, whichever context this machine has selected.
func requireRealTools(t *testing.T) {
	t.Helper()

	for _, tool := range []string{"helm", "kustomize"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	t.Setenv("KUBECONFIG", os.DevNull)
}

// writeTestFile writes content to path, creating parent directories.
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// writeKustomizeProject lays out a binnacle config with a kustomize block using
// both a patch file and an inline patch, plus an extra resource, and returns the
// config path. chartRef overrides the chart; empty means a generated local chart.
func writeKustomizeProject(t *testing.T, chartRef string) string {
	t.Helper()

	dir := t.TempDir()

	if chartRef == "" {
		chartRef = filepath.Join(dir, "chart")
		writeTestFile(t, filepath.Join(chartRef, "Chart.yaml"),
			"apiVersion: v2\nname: demo\nversion: 0.1.0\n")
		writeTestFile(t, filepath.Join(chartRef, "templates", "deployment.yaml"), `apiVersion: apps/v1
kind: Deployment
metadata:
  name: demo
spec:
  selector:
    matchLabels:
      app: demo
  template:
    metadata:
      labels:
        app: demo
    spec:
      containers:
        - name: demo
          image: example/demo:1.0.0
`)
	}

	writeTestFile(t, filepath.Join(dir, "extra-configmap.yml"), `apiVersion: v1
kind: ConfigMap
metadata:
  name: extra-from-kustomize
`)
	writeTestFile(t, filepath.Join(dir, "file-patch.yml"), `apiVersion: apps/v1
kind: Deployment
metadata:
  name: demo
  annotations:
    file-patch: applied
`)

	configPath := filepath.Join(dir, "config.yml")
	writeTestFile(t, configPath, fmt.Sprintf(`charts:
  - name: %s
    namespace: demo
    release: demo
    state: present
    values:
      unused: true
    kustomize:
      resources:
        - extra-configmap.yml
      patches:
        - path: file-patch.yml
          target:
            kind: Deployment
        - patch: |-
            - op: add
              path: /metadata/labels
              value:
                inline-patch: applied
          target:
            kind: Deployment
`, chartRef))

	return configPath
}

// useConfigFile loads configPath the way PreRunE does before a command runs.
func useConfigFile(t *testing.T, configPath string) {
	t.Helper()

	prev := cfgFile
	cfgFile = configPath
	t.Cleanup(func() {
		cfgFile = prev
		resetViperKeepingFlags()
	})

	if err := loadConfig(); err != nil {
		t.Fatalf("loadConfig(%s): %v", configPath, err)
	}
}

// captureStdout returns what fn printed along with the error it returned.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	realStdout := os.Stdout
	os.Stdout = w

	fnErr := fn()

	w.Close()
	os.Stdout = realStdout

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading captured stdout: %v", err)
	}

	return string(out), fnErr
}

// assertKustomized fails unless all three kustomize inputs reached the output,
// which also proves the post-renderer ran rather than being silently skipped.
func assertKustomized(t *testing.T, out string) {
	t.Helper()

	for _, want := range []string{"file-patch: applied", "inline-patch: applied", "extra-from-kustomize"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q\n%s", want, out)
		}
	}
}

func TestTemplateAppliesKustomizeWithRealHelm(t *testing.T) {
	requireRealTools(t)
	useConfigFile(t, writeKustomizeProject(t, ""))

	out, err := captureStdout(t, func() error { return templateCmdRun() })
	if err != nil {
		t.Fatalf("templateCmdRun: %v", err)
	}

	assertKustomized(t, out)
}

func TestSyncAppliesKustomizeWithRealHelm(t *testing.T) {
	requireRealTools(t)

	version, err := RunHelmCommand("version", "--template", "{{.Version}}")
	if err != nil {
		t.Fatalf("helm version: %s: %v", version.Stderr, err)
	}
	if strings.HasPrefix(version.Stdout, "v3.") {
		t.Skip("Helm 3 asks the cluster for its version even with --dry-run=client")
	}

	// The memory driver keeps release state out of any cluster, and helm reads
	// its seed file unconditionally, so that has to exist too.
	seed := filepath.Join(t.TempDir(), "releases.yaml")
	writeTestFile(t, seed, "[]\n")
	t.Setenv("HELM_DRIVER", "memory")
	t.Setenv("HELM_MEMORY_DRIVER_DATA", seed)

	useConfigFile(t, writeKustomizeProject(t, ""))

	out, err := captureStdout(t, func() error { return syncCmdRun("--dry-run=client") })
	if err != nil {
		t.Fatalf("syncCmdRun: %v", err)
	}

	assertKustomized(t, out)
}

func TestDiffAppliesKustomizeWithRealHelm(t *testing.T) {
	requireRealTools(t)

	installed, err := PluginInstalled("diff")
	if err != nil {
		t.Fatalf("PluginInstalled: %v", err)
	}
	if !installed {
		t.Skip("helm-diff plugin is not installed")
	}

	useConfigFile(t, writeKustomizeProject(t, ""))

	// --dry-run keeps helm-diff away from the cluster, but only with binnacle's
	// --three-way-merge turned back off, because that reads the live objects.
	// Passthrough arguments come last, so this one wins.
	out, err := captureStdout(t, func() error {
		return diffCmdRun("--dry-run", "--three-way-merge=false")
	})
	if err != nil {
		t.Fatalf("diffCmdRun: %v", err)
	}

	assertKustomized(t, out)
}

func TestKustomizeFailureReachesTheUserWithRealHelm(t *testing.T) {
	requireRealTools(t)

	configPath := writeKustomizeProject(t, "")
	writeTestFile(t, filepath.Join(filepath.Dir(configPath), "extra-configmap.yml"), "this: is: not: yaml\n")
	useConfigFile(t, configPath)

	_, err := captureStdout(t, func() error { return templateCmdRun() })
	if err == nil {
		t.Fatal("templateCmdRun succeeded on an invalid kustomize resource")
	}

	// Only kustomize's own message names the file. Helm 4 reports nothing more
	// than `plugin "..." exited with error`.
	if !strings.Contains(err.Error(), "extra-configmap.yml") {
		t.Errorf("error does not carry kustomize's message: %v", err)
	}
}

// TestGetterPluginsStayUsableWithRealHelm guards the shape of the HELM_PLUGINS
// value binnacle generates. Helm 4 finds cli and post-renderer plugins through a
// path list but looks for getter plugins in the whole value as one directory, so
// a list would quietly lose every getter.
func TestGetterPluginsStayUsableWithRealHelm(t *testing.T) {
	requireRealTools(t)

	plugins := t.TempDir()
	writeTestFile(t, filepath.Join(plugins, "testgetter", "plugin.yaml"), `name: testgetter
version: 0.1.0
downloaders:
  - command: "get.sh"
    protocols: ["binnacletest"]
`)
	writeTestFile(t, filepath.Join(plugins, "testgetter", "get.sh"),
		"#!/bin/sh\necho getter-was-called >&2\nexit 1\n")
	if err := os.Chmod(filepath.Join(plugins, "testgetter", "get.sh"), 0o755); err != nil {
		t.Fatalf("making getter executable: %v", err)
	}
	t.Setenv("HELM_PLUGINS", plugins)

	useConfigFile(t, writeKustomizeProject(t, "binnacletest://example/demo-0.1.0.tgz"))

	_, err := captureStdout(t, func() error { return templateCmdRun() })
	if err == nil {
		t.Fatal("templateCmdRun succeeded although the getter always fails")
	}
	if !strings.Contains(err.Error(), "getter-was-called") {
		t.Errorf("helm did not use the getter plugin: %v", err)
	}
}
