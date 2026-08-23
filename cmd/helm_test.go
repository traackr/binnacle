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

	calls := withHelm(t, helmOK(listOutput))

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

	if len(*calls) != 1 {
		t.Fatalf("made %d helm calls, want 1", len(*calls))
	}
	if got := strings.Join((*calls)[0], " "); got != "repo list" {
		t.Errorf("helm called with %q, want %q", got, "repo list")
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
			// ReleaseExists initialises exists=true and only flips it to false
			// when stderr is something OTHER than "Error: release: not found",
			// so a genuinely absent release still reports true. The condition
			// is inverted relative to the function's name. syncCharts relies on
			// the current behaviour to decide what to uninstall, so this test
			// pins it rather than asserting what the name implies.
			want: true,
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
