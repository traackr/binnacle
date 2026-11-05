package cmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Traackr/binnacle/config"
	"gopkg.in/yaml.v3"
)

// helmAt is a fake helm reporting the given version and plugin directory.
func helmAt(version, pluginsDir string) func(args ...string) (Result, error) {
	return func(args ...string) (Result, error) {
		switch strings.Join(args, " ") {
		case "version --template {{.Version}}":
			return Result{Stdout: version}, nil
		case "env HELM_PLUGINS":
			return Result{Stdout: pluginsDir + "\n"}, nil
		case "plugin list":
			return Result{Stdout: "NAME\tVERSION\ndiff\t3.15.15\n"}, nil
		}
		return Result{}, nil
	}
}

// kustomizeChart returns a chart whose kustomize block needs no companion files.
func kustomizeChart() config.ChartConfig {
	return config.ChartConfig{
		Name:    "demo",
		Release: "demo",
		Kustomize: config.BinnacleKustomization{
			Patches: []config.Patch{{Patch: "- op: remove\n  path: /spec/replicas\n"}},
		},
	}
}

func requireKustomize(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("kustomize"); err != nil {
		t.Skip("kustomize not on PATH; SetupKustomize requires it")
	}
}

func TestHelmMajorVersion(t *testing.T) {
	tests := []struct {
		name    string
		stdout  string
		want    int
		wantErr bool
	}{
		{name: "helm 3", stdout: "v3.17.3", want: 3},
		{name: "helm 4", stdout: "v4.3.1", want: 4},
		{name: "a later major", stdout: "v5.0.0-rc.1", want: 5},
		{name: "surrounding whitespace", stdout: " v4.3.1\n", want: 4},
		{name: "no leading v", stdout: "4.3.1", want: 4},
		{name: "empty output", stdout: "", wantErr: true},
		{name: "not a version", stdout: "unknown", wantErr: true},
		{name: "non-numeric major", stdout: "vX.1.0", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withHelm(t, helmOK(tc.stdout))

			got, err := helmMajorVersion()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("helmMajorVersion() = %d, want an error", got)
				}
				if !strings.Contains(err.Error(), "helm version") {
					t.Errorf("error does not say what it was doing: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("helmMajorVersion(): %v", err)
			}
			if got != tc.want {
				t.Errorf("helmMajorVersion() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestHelmMajorVersionReportsHelmFailure(t *testing.T) {
	withHelm(t, helmFailOn("version", ""))

	_, err := helmMajorVersion()
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error does not carry helm's failure: %v", err)
	}
}

func TestSetupPostRendererOnHelm3UsesTheScriptPath(t *testing.T) {
	requireKustomize(t)
	t.Setenv("HELM_PLUGINS", "/left/alone")
	calls := withHelm(t, helmAt("v3.17.3", "/never/asked"))

	dir := t.TempDir()
	flags, run, err := SetupPostRenderer(dir, filepath.Join(dir, "config.yml"), kustomizeChart())
	if err != nil {
		t.Fatalf("SetupPostRenderer: %v", err)
	}

	want := []string{"--post-renderer", filepath.Join(dir, "exec-kustomize.sh")}
	if strings.Join(flags, " ") != strings.Join(want, " ") {
		t.Errorf("flags = %v, want %v", flags, want)
	}

	// Helm 3 logs a load error for every v1 plugin it finds, so its plugin
	// directory must be left alone.
	var seen string
	prev := RunHelmCommand
	RunHelmCommand = func(args ...string) (Result, error) {
		seen = os.Getenv("HELM_PLUGINS")
		return prev(args...)
	}
	if _, err := run("template", "demo"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if seen != "/left/alone" {
		t.Errorf("HELM_PLUGINS during the call = %q, want it unchanged", seen)
	}
	for _, call := range *calls {
		if call[0] == "env" {
			t.Errorf("Helm 3 has no need to ask for its plugin directory: %v", call)
		}
	}
}

func TestSetupPostRendererOnHelm4GeneratesAPlugin(t *testing.T) {
	requireKustomize(t)

	base := t.TempDir()
	writeTestFile(t, filepath.Join(base, "real-plugin", "plugin.yaml"), "name: real\n")
	// `helm plugin install <local dir>` leaves a symlink, not a directory.
	linkTarget := t.TempDir()
	writeTestFile(t, filepath.Join(linkTarget, "plugin.yaml"), "name: linked\n")
	if err := os.Symlink(linkTarget, filepath.Join(base, "linked-plugin")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	withHelm(t, helmAt("v4.3.1", base))

	dir := t.TempDir()
	flags, run, err := SetupPostRenderer(dir, filepath.Join(dir, "config.yml"), kustomizeChart())
	if err != nil {
		t.Fatalf("SetupPostRenderer: %v", err)
	}

	if got, want := strings.Join(flags, " "), "--post-renderer "+postRendererPluginName; got != want {
		t.Errorf("flags = %q, want %q", got, want)
	}

	var pluginsDir string
	prev := RunHelmCommand
	RunHelmCommand = func(args ...string) (Result, error) {
		pluginsDir = os.Getenv("HELM_PLUGINS")
		return prev(args...)
	}
	if _, err := run("template", "demo"); err != nil {
		t.Fatalf("run: %v", err)
	}

	// One directory, never a list: Helm 4 reads the whole value as a single
	// directory when it looks for getter plugins.
	if pluginsDir == "" || len(filepath.SplitList(pluginsDir)) != 1 {
		t.Fatalf("HELM_PLUGINS = %q, want a single directory", pluginsDir)
	}

	for name, want := range map[string]string{"real-plugin": "name: real\n", "linked-plugin": "name: linked\n"} {
		got, err := os.ReadFile(filepath.Join(pluginsDir, name, "plugin.yaml"))
		if err != nil {
			t.Errorf("existing plugin %s is hidden from helm: %v", name, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s/plugin.yaml = %q, want %q", name, got, want)
		}
	}

	data, err := os.ReadFile(filepath.Join(pluginsDir, postRendererPluginName, "plugin.yaml"))
	if err != nil {
		t.Fatalf("generated plugin: %v", err)
	}
	var plugin struct {
		APIVersion    string `yaml:"apiVersion"`
		Name          string `yaml:"name"`
		Type          string `yaml:"type"`
		Runtime       string `yaml:"runtime"`
		Version       string `yaml:"version"`
		RuntimeConfig struct {
			PlatformCommand []struct {
				Command string `yaml:"command"`
			} `yaml:"platformCommand"`
		} `yaml:"runtimeConfig"`
	}
	if err := yaml.Unmarshal(data, &plugin); err != nil {
		t.Fatalf("generated plugin.yaml is not valid yaml: %v", err)
	}
	if plugin.APIVersion != "v1" || plugin.Type != "postrenderer/v1" || plugin.Runtime != "subprocess" {
		t.Errorf("plugin identity = %+v", plugin)
	}
	if plugin.Name != postRendererPluginName || plugin.Version == "" {
		t.Errorf("plugin name/version = %q/%q", plugin.Name, plugin.Version)
	}
	script := filepath.Join(dir, "exec-kustomize.sh")
	if len(plugin.RuntimeConfig.PlatformCommand) != 1 || plugin.RuntimeConfig.PlatformCommand[0].Command != script {
		t.Errorf("platformCommand = %+v, want the single command %s", plugin.RuntimeConfig.PlatformCommand, script)
	}
}

func TestSetupPostRendererRestoresHelmPluginsAfterTheCall(t *testing.T) {
	requireKustomize(t)

	t.Run("previously set", func(t *testing.T) {
		t.Setenv("HELM_PLUGINS", "/was/set")
		withHelm(t, helmAt("v4.3.1", t.TempDir()))

		dir := t.TempDir()
		_, run, err := SetupPostRenderer(dir, filepath.Join(dir, "config.yml"), kustomizeChart())
		if err != nil {
			t.Fatalf("SetupPostRenderer: %v", err)
		}
		if _, err := run("template"); err != nil {
			t.Fatalf("run: %v", err)
		}
		if got := os.Getenv("HELM_PLUGINS"); got != "/was/set" {
			t.Errorf("HELM_PLUGINS = %q after the call, want it restored", got)
		}
	})

	t.Run("previously unset", func(t *testing.T) {
		t.Setenv("HELM_PLUGINS", "")
		os.Unsetenv("HELM_PLUGINS")
		withHelm(t, helmAt("v4.3.1", t.TempDir()))

		dir := t.TempDir()
		_, run, err := SetupPostRenderer(dir, filepath.Join(dir, "config.yml"), kustomizeChart())
		if err != nil {
			t.Fatalf("SetupPostRenderer: %v", err)
		}
		if _, err := run("template"); err != nil {
			t.Fatalf("run: %v", err)
		}
		if _, set := os.LookupEnv("HELM_PLUGINS"); set {
			t.Error("HELM_PLUGINS is set after the call, want it unset again")
		}
	})
}

func TestSetupPostRendererMergesAPluginPathList(t *testing.T) {
	requireKustomize(t)

	first, second := t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(first, "shared", "plugin.yaml"), "name: from-first\n")
	writeTestFile(t, filepath.Join(second, "shared", "plugin.yaml"), "name: from-second\n")
	writeTestFile(t, filepath.Join(second, "only-second", "plugin.yaml"), "name: only-second\n")
	withHelm(t, helmAt("v4.3.1", first+string(filepath.ListSeparator)+second))

	dir := t.TempDir()
	if _, _, err := SetupPostRenderer(dir, filepath.Join(dir, "config.yml"), kustomizeChart()); err != nil {
		t.Fatalf("SetupPostRenderer: %v", err)
	}

	merged := filepath.Join(dir, "helm-plugins")
	got, err := os.ReadFile(filepath.Join(merged, "shared", "plugin.yaml"))
	if err != nil {
		t.Fatalf("shared plugin: %v", err)
	}
	if string(got) != "name: from-first\n" {
		t.Errorf("shared plugin = %q, want the first directory to win, as in a path lookup", got)
	}
	if _, err := os.Stat(filepath.Join(merged, "only-second", "plugin.yaml")); err != nil {
		t.Errorf("plugin from the second directory is hidden: %v", err)
	}
}

func TestSetupPostRendererLinksARelativePluginDirectory(t *testing.T) {
	requireKustomize(t)

	// A symlink's target resolves against the link's own directory, so a relative
	// HELM_PLUGINS copied as given would leave every plugin behind a dangling link.
	t.Chdir(t.TempDir())
	writeTestFile(t, filepath.Join("relative-plugins", "real-plugin", "plugin.yaml"), "name: real\n")
	withHelm(t, helmAt("v4.3.1", "relative-plugins"))

	dir := t.TempDir()
	if _, _, err := SetupPostRenderer(dir, filepath.Join(dir, "config.yml"), kustomizeChart()); err != nil {
		t.Fatalf("SetupPostRenderer: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "helm-plugins", "real-plugin", "plugin.yaml"))
	if err != nil {
		t.Fatalf("plugin from a relative HELM_PLUGINS is hidden from helm: %v", err)
	}
	if string(got) != "name: real\n" {
		t.Errorf("plugin.yaml = %q", got)
	}
}

func TestSetupPostRendererToleratesAMissingPluginDirectory(t *testing.T) {
	requireKustomize(t)

	// A fresh helm install has no plugin directory until the first install.
	withHelm(t, helmAt("v4.3.1", filepath.Join(t.TempDir(), "does-not-exist")))

	dir := t.TempDir()
	if _, _, err := SetupPostRenderer(dir, filepath.Join(dir, "config.yml"), kustomizeChart()); err != nil {
		t.Fatalf("SetupPostRenderer: %v", err)
	}
}

func TestSetupPostRendererErrors(t *testing.T) {
	requireKustomize(t)

	notADirectory := filepath.Join(t.TempDir(), "file")
	writeTestFile(t, notADirectory, "")

	tests := []struct {
		name  string
		helm  func(args ...string) (Result, error)
		chart config.ChartConfig
		setup func(t *testing.T, dir string)
		want  string
	}{
		{
			name:  "kustomize setup fails",
			helm:  helmAt("v4.3.1", t.TempDir()),
			chart: config.ChartConfig{Kustomize: config.BinnacleKustomization{Resources: []string{"missing.yml"}}},
			want:  "reading kustomize resource file",
		},
		{
			name:  "helm version fails",
			helm:  helmFailOn("version", ""),
			chart: kustomizeChart(),
			want:  "helm version",
		},
		{
			name:  "helm version is unreadable",
			helm:  helmAt("not-a-version", t.TempDir()),
			chart: kustomizeChart(),
			want:  "helm version",
		},
		{
			name:  "helm env fails",
			helm:  helmFailOn("env", "v4.3.1"),
			chart: kustomizeChart(),
			want:  "helm plugin directory",
		},
		{
			name:  "plugin directory cannot be read",
			helm:  helmAt("v4.3.1", notADirectory),
			chart: kustomizeChart(),
			want:  "helm plugin directory",
		},
		{
			name:  "generated plugin cannot be written",
			helm:  helmAt("v4.3.1", t.TempDir()),
			chart: kustomizeChart(),
			setup: func(t *testing.T, dir string) { writeTestFile(t, filepath.Join(dir, "helm-plugins"), "") },
			want:  "post-renderer plugin",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withHelm(t, tc.helm)

			dir := t.TempDir()
			if tc.setup != nil {
				tc.setup(t, dir)
			}

			_, _, err := SetupPostRenderer(dir, filepath.Join(dir, "config.yml"), tc.chart)
			if err == nil {
				t.Fatal("SetupPostRenderer succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestPostRendererRunAddsKustomizeErrors(t *testing.T) {
	requireKustomize(t)

	tests := []struct {
		name       string
		kustomize  string
		helmErr    error
		wantStderr string
	}{
		{
			name:       "failure with kustomize output",
			kustomize:  "Error: accumulating resources\n",
			helmErr:    errors.New("exit status 1"),
			wantStderr: "Error: plugin exited with error\nkustomize: Error: accumulating resources",
		},
		{
			name:       "failure before kustomize ran",
			helmErr:    errors.New("exit status 1"),
			wantStderr: "Error: plugin exited with error\n",
		},
		{
			name:       "success leaves kustomize warnings out",
			kustomize:  "# Warning: 'bases' is deprecated\n",
			wantStderr: "Error: plugin exited with error\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withHelm(t, helmAt("v4.3.1", t.TempDir()))

			dir := t.TempDir()
			_, run, err := SetupPostRenderer(dir, filepath.Join(dir, "config.yml"), kustomizeChart())
			if err != nil {
				t.Fatalf("SetupPostRenderer: %v", err)
			}
			if tc.kustomize != "" {
				writeTestFile(t, filepath.Join(dir, kustomizeStderrFile), tc.kustomize)
			}

			RunHelmCommand = func(...string) (Result, error) {
				return Result{Stderr: "Error: plugin exited with error\n"}, tc.helmErr
			}

			res, err := run("template")
			if !errors.Is(err, tc.helmErr) {
				t.Errorf("err = %v, want %v", err, tc.helmErr)
			}
			if res.Stderr != tc.wantStderr {
				t.Errorf("Stderr = %q, want %q", res.Stderr, tc.wantStderr)
			}
		})
	}
}

func TestSetupKustomizeScriptCapturesStderr(t *testing.T) {
	requireKustomize(t)

	dir := t.TempDir()
	script, err := SetupKustomize(dir, filepath.Join(dir, "config.yml"), kustomizeChart())
	if err != nil {
		t.Fatalf("SetupKustomize: %v", err)
	}
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("reading script: %v", err)
	}
	if want := "2> " + filepath.Join(dir, kustomizeStderrFile); !strings.Contains(string(data), want) {
		t.Errorf("script does not redirect stderr with %q:\n%s", want, data)
	}
}

// kustomizeCommands lists every command that hands a chart to a post-renderer.
var kustomizeCommands = []struct {
	name   string
	prefix string
	run    func(args ...string) error
}{
	{"template", "template platform-api", templateCmdRun},
	{"sync", "upgrade platform-api", syncCmdRun},
	{"diff", "diff upgrade platform-api", diffCmdRun},
}

func TestCommandsUseTheGeneratedPluginOnHelm4(t *testing.T) {
	requireKustomize(t)

	for _, tc := range kustomizeCommands {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HELM_PLUGINS", "/original")
			loadFixture(t, "kustomize/config.yml")

			pluginsSeen := map[string]string{}
			helm := helmAt("v4.3.1", t.TempDir())
			calls := withHelm(t, func(args ...string) (Result, error) {
				pluginsSeen[strings.Join(args, " ")] = os.Getenv("HELM_PLUGINS")
				return helm(args...)
			})

			if err := tc.run("--kube-context", "ctx"); err != nil {
				t.Fatalf("command: %v", err)
			}

			var joined []string
			for _, c := range *calls {
				joined = append(joined, strings.Join(c, " "))
			}
			got := findCall(t, joined, tc.prefix)
			if !strings.Contains(got, "--post-renderer "+postRendererPluginName+" ") {
				t.Errorf("args do not name the generated plugin\n  got: %s", got)
			}
			if !strings.HasSuffix(got, "--kube-context ctx") {
				t.Errorf("passthrough is not last\n  got: %s", got)
			}
			if seen := pluginsSeen[got]; !strings.HasSuffix(seen, "helm-plugins") {
				t.Errorf("HELM_PLUGINS during the render = %q", seen)
			}

			// Only the render gets the generated directory. Anything else keeps
			// the user's, including the repo commands that ran before it.
			for call, seen := range pluginsSeen {
				if call != got && seen != "/original" {
					t.Errorf("%q ran with HELM_PLUGINS = %q", call, seen)
				}
			}
		})
	}
}

func TestCommandsUseTheScriptPathOnHelm3(t *testing.T) {
	requireKustomize(t)

	for _, tc := range kustomizeCommands {
		t.Run(tc.name, func(t *testing.T) {
			loadFixture(t, "kustomize/config.yml")
			calls := withHelm(t, helmAt("v3.17.3", "/never/asked"))

			if err := tc.run(); err != nil {
				t.Fatalf("command: %v", err)
			}

			var joined []string
			for _, c := range *calls {
				joined = append(joined, strings.Join(c, " "))
			}
			got := findCall(t, joined, tc.prefix)
			if !strings.Contains(got, "exec-kustomize.sh") {
				t.Errorf("args do not name the script\n  got: %s", got)
			}
		})
	}
}

func TestCommandsPropagatePostRendererSetupErrors(t *testing.T) {
	requireKustomize(t)

	for _, tc := range kustomizeCommands {
		t.Run(tc.name, func(t *testing.T) {
			loadFixture(t, "kustomize/config.yml")
			withHelm(t, helmAt("not-a-version", t.TempDir()))

			err := tc.run()
			if err == nil || !strings.Contains(err.Error(), "helm version") {
				t.Fatalf("error = %v, want it to mention the helm version", err)
			}
		})
	}
}

func TestCommandsDoNotProbeHelmWithoutKustomize(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(args ...string) error
	}{
		{"template", templateCmdRun},
		{"sync", syncCmdRun},
		{"diff", diffCmdRun},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loadFixture(t, "demo.yml")
			calls := withHelm(t, helmAt("not-a-version", "/never/asked"))

			if err := tc.run(); err != nil {
				t.Fatalf("command: %v", err)
			}
			for _, c := range *calls {
				if c[0] == "version" || c[0] == "env" {
					t.Errorf("a chart without kustomize should not cost a helm probe: %v", c)
				}
			}
		})
	}
}

func TestDiffForcesColorThroughTheEnvironment(t *testing.T) {
	// Helm 4 has a global --color that takes a value, so the flag would swallow
	// whichever argument came next.
	tests := []struct {
		name string
		set  bool
		was  string
		want string
	}{
		{name: "defaults on", want: "true"},
		{name: "keeps the user's choice", set: true, was: "false", want: "false"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HELM_DIFF_COLOR", tc.was)
			if !tc.set {
				os.Unsetenv("HELM_DIFF_COLOR")
			}
			loadFixture(t, "demo.yml")

			var seen string
			helm := helmAt("v4.3.1", "")
			calls := withHelm(t, func(args ...string) (Result, error) {
				if args[0] == "diff" {
					seen = os.Getenv("HELM_DIFF_COLOR")
				}
				return helm(args...)
			})

			if err := diffCmdRun(); err != nil {
				t.Fatalf("diffCmdRun: %v", err)
			}
			if seen != tc.want {
				t.Errorf("HELM_DIFF_COLOR during diff = %q, want %q", seen, tc.want)
			}
			for _, c := range *calls {
				if strings.Contains(strings.Join(c, " "), "--color") {
					t.Errorf("--color must not reach helm: %v", c)
				}
			}

			got, set := os.LookupEnv("HELM_DIFF_COLOR")
			if set != tc.set || got != tc.was {
				t.Errorf("HELM_DIFF_COLOR after diff = %q (set=%v), want it restored", got, set)
			}
		})
	}
}
