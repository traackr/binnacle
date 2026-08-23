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
