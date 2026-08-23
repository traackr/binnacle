// Copyright © 2018 Anthony Spring <aspring@traackr.com>
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Traackr/binnacle/config"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

var cfgFile string

// log The general purpose logging interface available to all commands
var log = logrus.New()

// GITCOMMIT The gitcommit the application was built from
var GITCOMMIT string

// VERSION The version of the application
var VERSION string

// RootCmd represents the base command when called without any subcommands
var RootCmd = &cobra.Command{
	Use:          "binnacle",
	Short:        "An opinionated automation tool for Kubernetes' Helm.",
	Long:         ``,
	SilenceUsage: true,
	Version:      fmt.Sprintf("%s-%s", VERSION, GITCOMMIT),
}

// Execute adds all child commands to the root command sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	// General Flags
	RootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "The Binnacle config file (required)")

	// Logging Flags
	RootCmd.PersistentFlags().String("loglevel", "info", "The level of logging. Acceptable values: debug, info, warn, error, fatal, panic.")
	viper.BindPFlag("loglevel", RootCmd.PersistentFlags().Lookup("loglevel"))
}

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

// PluginInstalled returns if the given plugin is installed
func PluginInstalled(plugin string) (bool, error) {
	var err error
	var output []string
	var res Result

	// Get a list of currently installed plugins
	res, err = RunHelmCommand("plugin", "list")
	if err != nil {
		return false, fmt.Errorf("running helm plugin list: %s: %w", res.Stderr, err)
	}

	// Split the output on the new line
	output = strings.Split(res.Stdout, "\n")

	// Remove the column titles
	if len(output) > 0 {
		output = output[1:]
	}

	// Iterate the plugins
	for _, line := range output {
		if len(line) == 0 {
			continue
		}

		// Split the string by a space
		split := strings.Fields(line)

		if plugin == split[0] {
			return true, nil
		}
	}

	return false, nil
}

func ReleaseExists(namespace string, release string, args ...string) bool {
	var exists = true
	var err error
	var res Result
	var cmdArgs []string

	cmdArgs = append(cmdArgs, "status")
	cmdArgs = append(cmdArgs, release)
	cmdArgs = append(cmdArgs, "--namespace")
	cmdArgs = append(cmdArgs, namespace)
	cmdArgs = append(cmdArgs, args...)

	// Get the status of the release for the namespace
	res, err = RunHelmCommand(cmdArgs...)
	if err != nil {
		if res.Stderr != "Error: release: not found" {
			exists = false
		}
	}

	return exists
}

// RunHelmCommand runs the given command against helm.
//
// It is a variable rather than a function so tests can substitute the process
// call. Every command in this package funnels through it, so without this seam
// none of them can be exercised without a real helm binary on PATH.
var RunHelmCommand = execHelm

func execHelm(args ...string) (Result, error) {
	var result Result
	var outbuf, errbuf bytes.Buffer

	helm, err := exec.LookPath("helm")
	if err != nil {
		return result, fmt.Errorf("searching for helm on PATH: %w", err)
	}
	cmd := exec.Command(helm, args...)

	log.Debugf("Executing command:  %v", cmd.Args)

	cmd.Stdout = &outbuf
	cmd.Stderr = &errbuf

	err = cmd.Run()

	log.Debugf("Execution complete.")

	result.Stdout = strings.Trim(outbuf.String(), " ")
	result.Stderr = strings.Trim(errbuf.String(), " ")

	if err != nil {
		//
		// This crafty snippet is from https://stackoverflow.com/a/55055100
		//
		if exitError, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitError.ExitCode()
		} else {
			if result.Stderr == "" {
				result.Stderr = err.Error()
			}
		}
	}

	return result, err
}

func syncRepositories(repos []config.RepositoryConfig, args ...string) error {
	var reposModified = false

	for _, repo := range repos {
		var cmdArgs []string
		var err error
		var res Result
		var currentRepos []config.RepositoryConfig

		log.Debugf("Processing repo: %s", repo.Name)

		currentRepos, err = getCurrentRepositories()
		if err != nil {
			return err
		}

		repoExists, repoFullMatch := repoExists(repo, currentRepos)

		// If the repo exists and is not a full match, or we are deleting the repo - we need to delete the repo
		if repoExists && (!repoFullMatch || repo.State != config.StatePresent) {
			cmdArgs = append(cmdArgs, "repo")
			cmdArgs = append(cmdArgs, "remove")
			cmdArgs = append(cmdArgs, args...)
			cmdArgs = append(cmdArgs, repo.Name)

			res, err = RunHelmCommand(cmdArgs...)
			if err != nil {
				return fmt.Errorf("running helm repo remove: %s: %w", res.Stderr, err)
			}
		}
		cmdArgs = nil

		if repo.State == config.StatePresent {
			cmdArgs = append(cmdArgs, "repo")
			cmdArgs = append(cmdArgs, "add")
			cmdArgs = append(cmdArgs, args...)
			cmdArgs = append(cmdArgs, repo.Name)
			cmdArgs = append(cmdArgs, repo.URL)

			res, err = RunHelmCommand(cmdArgs...)
			if err != nil {
				return fmt.Errorf("running helm repo add: %s: %w", res.Stderr, err)
			}
			reposModified = true

			fmt.Println(strings.TrimSpace(res.Stdout))
		}
	}

	// If any repos have been added during this sync execute a helm repos update to update the cache.
	if reposModified {
		var cmdArgs []string
		var err error
		var res Result

		cmdArgs = append(cmdArgs, "repo")
		cmdArgs = append(cmdArgs, "update")
		res, err = RunHelmCommand(cmdArgs...)
		if err != nil {
			return fmt.Errorf("running helm repo update: %s: %w", res.Stderr, err)
		}
		fmt.Println(strings.TrimSpace(res.Stdout))
	}

	return nil
}

func getCurrentRepositories() ([]config.RepositoryConfig, error) {
	var err error
	var output []string
	var repos []config.RepositoryConfig
	var res Result

	// Get a list of currently configured repositories
	res, err = RunHelmCommand("repo", "list")
	if err != nil {
		return nil, fmt.Errorf("running helm repo list: %s: %w", res.Stderr, err)
	}

	// Split the output on the new line
	output = strings.Split(res.Stdout, "\n")

	// Remove the column titles
	if len(output) > 0 {
		output = output[1:]
	}

	// Populate the repos
	for _, line := range output {
		var repo config.RepositoryConfig

		if len(line) == 0 {
			continue
		}

		// Split the string by a space
		split := strings.Fields(line)

		// Build the repository config
		repo.Name = split[0]
		repo.URL = split[1]

		repos = append(repos, repo)
	}

	return repos, nil
}

func repoExists(repo config.RepositoryConfig, repos []config.RepositoryConfig) (bool, bool) {
	var exists = false
	var fullMatch = false

	// Check if this repo already exists
	for _, checkRepo := range repos {
		// Check if the repos are equal
		if repo.Equal(checkRepo) {
			exists = true
			fullMatch = true
			break
		} else {
			// Check if their names match but URLs are different
			if repo.Name == checkRepo.Name {
				exists = true
				if repo.URL != checkRepo.URL {
					fullMatch = true
				}
				break
			}
		}

	}

	return exists, fullMatch
}

func SetupBinnacleWorkingDir() (string, error) {
	dir, err := os.MkdirTemp("", "binnacle-exec")
	if err != nil {
		return "", fmt.Errorf("creating temporary working directory: %w", err)
	}

	return dir, nil
}

// Set up the kustomize post-renderer script and kustomization.yml
func SetupKustomize(tmpDir string, configPath string, chart config.ChartConfig) (string, error) {
	kustomize, err := exec.LookPath("kustomize")
	if err != nil {
		return "", fmt.Errorf("configuring kustomize: kustomize was not installed")
	}

	// Use a random filename to prevent conflicts with any actual filenames
	helmTemplateFilename := fmt.Sprintf("%s.yml", uuid.New())
	// Script that reads stdin (result of helm template) and runs kustomize,
	// which will write the result to stdout, returning it to Helm
	// NOTE: The script will be executed by Helm, using the current PATH and current working directory
	script := fmt.Sprintf(`#!/bin/sh
cat > %s
exec %s build %s
`, filepath.Join(tmpDir, helmTemplateFilename), kustomize, tmpDir)
	scriptPath := filepath.Join(tmpDir, "exec-kustomize.sh")
	err = os.WriteFile(scriptPath, []byte(script), 0755)
	if err != nil {
		return "", fmt.Errorf("writing exec-kustomize script: %w", err)
	}

	binnacleFilesDir := filepath.Dir(configPath)

	// Fix relative paths (relative to binnacle dir) to be accessible from the tmp dir
	resources := chart.Kustomize.Resources
	for i, r := range resources {
		resourcePath := filepath.Join(binnacleFilesDir, r)
		data, err := os.ReadFile(resourcePath)
		if err != nil {
			return "", fmt.Errorf("reading kustomize resource file: %w", err)
		}
		tmpResourcePath := filepath.Join(tmpDir, filepath.Base(r))
		err = os.WriteFile(tmpResourcePath, data, 0644)
		if err != nil {
			return "", fmt.Errorf("writing temporary kustomize resource file: %w", err)
		}
		resources[i] = filepath.Base(r)
	}
	// Add in the file with the helm-templated contents
	resources = append(resources, helmTemplateFilename)

	patches := chart.Kustomize.Patches
	if len(patches) == 0 {
		patches = make([]config.Patch, 0)
	}
	for i, p := range patches {
		if len(p.Path) == 0 {
			continue
		}

		patchPath := filepath.Join(binnacleFilesDir, p.Path)
		data, err := os.ReadFile(patchPath)
		if err != nil {
			return "", fmt.Errorf("reading kustomize patch file: %w", err)
		}
		tmpPatchPath := filepath.Join(tmpDir, filepath.Base(p.Path))
		err = os.WriteFile(tmpPatchPath, data, 0644)
		if err != nil {
			return "", fmt.Errorf("writing temporary kustomize patch file: %w", err)
		}
		patches[i].Path = filepath.Base(p.Path)
	}

	kustomizationData, err := yaml.Marshal(config.BinnacleKustomization{
		Resources: resources,
		Patches:   patches,
	})
	if err != nil {
		return "", fmt.Errorf("marshalling generated kustomization.yml to yaml: %w", err)
	}

	kustomizationYmlPath := filepath.Join(tmpDir, "kustomization.yml")
	log.Debugf("kustomization.yml: \n%s", string(kustomizationData))
	err = os.WriteFile(kustomizationYmlPath, kustomizationData, 0644)
	if err != nil {
		return "", fmt.Errorf("writing generated kustomization.yml: %w", err)
	}

	return scriptPath, nil
}
