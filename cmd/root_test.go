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
	t.Cleanup(func() {
		cfgFile = prev
		viper.Reset()
		// viper.Reset() also discards the loglevel pflag binding made once in
		// root.go's init(), so any later loadConfig() call would otherwise fail
		// parsing an empty loglevel. Rebinding here restores the state Reset
		// just wiped.
		viper.BindPFlag("loglevel", RootCmd.PersistentFlags().Lookup("loglevel"))
	})

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
