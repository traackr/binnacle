package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Traackr/binnacle/config"
	"gopkg.in/yaml.v3"
)

const (
	// postRendererPluginName is the plugin binnacle generates for Helm 4.
	postRendererPluginName = "binnacle-kustomize"

	// kustomizeStderrFile holds kustomize's stderr inside the working directory.
	// Helm 4 discards a plugin's stderr and reports only that it exited with an
	// error, so this is the only place kustomize's own message survives.
	kustomizeStderrFile = "kustomize-stderr.log"

	// helmPluginsDirName is the directory HELM_PLUGINS points at on Helm 4.
	helmPluginsDirName = "helm-plugins"
)

// SetupPostRenderer prepares kustomize as the post-renderer for one helm call. It
// returns the flags to add to that call and the function to make the call with.
//
// Helm 3 takes the path of an executable. Helm 4 takes only the name of a
// postrenderer plugin, so there binnacle generates one in its working directory
// and points HELM_PLUGINS at it for the duration of the call.
func SetupPostRenderer(tmpDir string, configPath string, chart config.ChartConfig) ([]string, func(...string) (Result, error), error) {
	script, err := SetupKustomize(tmpDir, configPath, chart)
	if err != nil {
		return nil, nil, err
	}

	major, err := helmMajorVersion()
	if err != nil {
		return nil, nil, err
	}

	flags := []string{"--post-renderer", script}
	var pluginsDir string
	if major >= 4 {
		pluginsDir, err = setupPluginsDir(tmpDir, script)
		if err != nil {
			return nil, nil, err
		}
		flags[1] = postRendererPluginName
	}

	run := func(args ...string) (Result, error) {
		if pluginsDir != "" {
			defer setenvTemporarily("HELM_PLUGINS", pluginsDir)()
		}

		res, err := RunHelmCommand(args...)
		if err != nil {
			res.Stderr = withKustomizeStderr(res.Stderr, tmpDir)
		}

		return res, err
	}

	return flags, run, nil
}

// helmMajorVersion returns the major version of the helm on PATH. It fails rather
// than guesses, because the wrong guess turns into a confusing flag error.
func helmMajorVersion() (int, error) {
	res, err := RunHelmCommand("version", "--template", "{{.Version}}")
	if err != nil {
		return 0, fmt.Errorf("reading helm version: %s: %w", res.Stderr, err)
	}

	version := strings.TrimSpace(res.Stdout)
	major, _, _ := strings.Cut(strings.TrimPrefix(version, "v"), ".")
	n, err := strconv.Atoi(major)
	if err != nil {
		return 0, fmt.Errorf("reading helm version: cannot parse %q", version)
	}

	return n, nil
}

// setupPluginsDir creates the directory HELM_PLUGINS points at on Helm 4 and
// returns its path. It holds the generated plugin and a symlink to everything in
// the user's own plugin directories, so `helm diff` and any other plugin keep
// working during the call.
//
// It is one directory rather than a path list because Helm 4 reads HELM_PLUGINS
// as a single directory when it looks for getter plugins, so a list would lose
// them. Nothing here touches the user's plugin directory, and each chart has its
// own working directory, so parallel runs cannot collide.
func setupPluginsDir(tmpDir string, script string) (string, error) {
	res, err := RunHelmCommand("env", "HELM_PLUGINS")
	if err != nil {
		return "", fmt.Errorf("finding the helm plugin directory: %s: %w", res.Stderr, err)
	}

	pluginsDir := filepath.Join(tmpDir, helmPluginsDirName)
	if err := writePostRendererPlugin(filepath.Join(pluginsDir, postRendererPluginName), script); err != nil {
		return "", fmt.Errorf("writing post-renderer plugin: %w", err)
	}

	for _, dir := range filepath.SplitList(strings.TrimSpace(res.Stdout)) {
		// A symlink's target resolves against the link's own directory, so a
		// relative HELM_PLUGINS has to become absolute before it is linked.
		absDir, err := filepath.Abs(dir)
		if err != nil {
			return "", fmt.Errorf("reading helm plugin directory %s: %w", dir, err)
		}

		entries, err := os.ReadDir(absDir)
		if errors.Is(err, fs.ErrNotExist) {
			continue // A fresh helm install has no plugin directory yet.
		}
		if err != nil {
			return "", fmt.Errorf("reading helm plugin directory %s: %w", dir, err)
		}

		for _, entry := range entries {
			// An existing link means an earlier directory already provided this
			// name, and the earlier directory wins, as in any path lookup.
			err := os.Symlink(filepath.Join(absDir, entry.Name()), filepath.Join(pluginsDir, entry.Name()))
			if err != nil && !errors.Is(err, fs.ErrExist) {
				return "", fmt.Errorf("linking helm plugin %s: %w", entry.Name(), err)
			}
		}
	}

	return pluginsDir, nil
}

// writePostRendererPlugin writes a Helm 4 plugin that runs script. Helm pipes the
// rendered manifests to its stdin and reads the result from its stdout, which is
// what the script already does for Helm 3.
func writePostRendererPlugin(pluginDir string, script string) error {
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		return err
	}

	metadata, err := yaml.Marshal(map[string]any{
		"apiVersion": "v1",
		"name":       postRendererPluginName,
		"type":       "postrenderer/v1",
		"runtime":    "subprocess",
		"version":    "1.0.0",
		"runtimeConfig": map[string]any{
			"platformCommand": []map[string]string{{"command": script}},
		},
	})
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), metadata, 0o644)
}

// withKustomizeStderr appends what kustomize wrote to stderr, if anything, to the
// stderr helm reported for a failed call.
func withKustomizeStderr(helmStderr string, tmpDir string) string {
	data, err := os.ReadFile(filepath.Join(tmpDir, kustomizeStderrFile))
	if err != nil {
		return helmStderr
	}

	kustomizeStderr := strings.TrimSpace(string(data))
	if kustomizeStderr == "" {
		return helmStderr
	}

	return strings.TrimSpace(helmStderr) + "\nkustomize: " + kustomizeStderr
}

// setenvTemporarily sets an environment variable for helm child processes and
// returns a function that puts back whatever was there before.
func setenvTemporarily(key string, value string) func() {
	previous, wasSet := os.LookupEnv(key)
	os.Setenv(key, value)

	return func() {
		if wasSet {
			os.Setenv(key, previous)
		} else {
			os.Unsetenv(key)
		}
	}
}
