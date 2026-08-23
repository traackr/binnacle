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
	"fmt"
	"os"
	"strings"

	"github.com/Traackr/binnacle/config"
	"github.com/spf13/cobra"
)

// diffCmd represents the diff command
var diffCmd = &cobra.Command{
	Use:   "diff",
	Short: "Displays a diff between the current release and new release of a Helm chart.  (Requires helm-diff plugin)",
	Long:  ``,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if err := loadConfig(); err != nil {
			return err
		}
		diffCmdPreRun()

		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return diffCmdRun(args...)
	},
	PostRun: func(cmd *cobra.Command, args []string) {
		diffCmdPostRun()
	},
}

func init() {
	RootCmd.AddCommand(diffCmd)
}

func diffCmdPreRun() {
	log.Debug("Executing `diff` command.")
}

func diffCmdRun(args ...string) error {
	var err error

	// Detect if the diff plugin is installed.
	pluginInstalled, err := PluginInstalled("diff")
	if err != nil {
		return fmt.Errorf("detecting if helm-diff plugin is installed: %w", err)
	}

	if !pluginInstalled {
		return fmt.Errorf("checking for helm-diff plugin: helm-diff plugin is required, Please see: https://github.com/databus23/helm-diff")
	}

	// Load our configuration
	c, err := config.LoadAndValidateFromViper()
	if err != nil {
		return err
	}

	// Sync repositories
	if err := syncRepositories(c.Repositories, args...); err != nil {
		return err
	}

	var charts = c.Charts

	log.Debugf("Loaded %d charts.", len(charts))

	// Iterate the charts in the config
	for _, chart := range charts {
		var cmdArgs []string
		var res Result

		log.Debugf("Processing chart: %s", chart.ChartURL())

		// Create a temp working directory
		dir, err := SetupBinnacleWorkingDir()
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)

		//
		// Template out the charts values
		//
		valuesFile, err := chart.WriteValueFile(dir)
		if err != nil {
			return err
		}

		cmdArgs = append(cmdArgs, "diff")
		cmdArgs = append(cmdArgs, "upgrade")
		cmdArgs = append(cmdArgs, chart.Release)
		cmdArgs = append(cmdArgs, chart.ChartURL())
		cmdArgs = append(cmdArgs, "--color")
		cmdArgs = append(cmdArgs, "--normalize-manifests")
		cmdArgs = append(cmdArgs, "--install")
		cmdArgs = append(cmdArgs, "--three-way-merge")
		cmdArgs = append(cmdArgs, "--values")
		cmdArgs = append(cmdArgs, valuesFile)

		if len(chart.Namespace) > 0 {
			cmdArgs = append(cmdArgs, "--namespace")
			cmdArgs = append(cmdArgs, chart.Namespace)
		}

		if len(chart.Version) > 0 {
			cmdArgs = append(cmdArgs, "--version")
			cmdArgs = append(cmdArgs, chart.Version)
		}

		if !chart.Kustomize.Empty() {
			postRenderExecutable, err := SetupKustomize(dir, c.ConfigFile, chart)
			if err != nil {
				return err
			}
			cmdArgs = append(cmdArgs, "--post-renderer")
			cmdArgs = append(cmdArgs, postRenderExecutable)
		}

		cmdArgs = append(cmdArgs, args...)
		res, err = RunHelmCommand(cmdArgs...)
		if err != nil {
			return fmt.Errorf("running helm diff for release %s: %s: %w", chart.Release, res.Stderr, err)
		}

		fmt.Println(strings.TrimSpace(res.Stdout))
	}

	return nil
}

func diffCmdPostRun() {
	log.Debug("Execution of the `diff` command has completed.")
}
