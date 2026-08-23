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

// templateCmd represents the template command
var templateCmd = &cobra.Command{
	Use:   "template",
	Short: "Templates out each release, with values, from a given Binnacle configuration.",
	Long:  ``,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if err := loadConfig(); err != nil {
			return err
		}
		templateCmdPreRun()

		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return templateCmdRun(args...)
	},
	PostRun: func(cmd *cobra.Command, args []string) {
		templateCmdPostRun()
	},
}

func init() {
	RootCmd.AddCommand(templateCmd)
}

func templateCmdPreRun() {
	log.Debug("Executing `template` command.")
}

func templateCmdRun(args ...string) error {
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

	var absentCharts []string

	log.Debugf("Loaded %d charts.", len(charts))

	// Iterate the charts in the config
	for _, chart := range charts {
		var cmdArgs []string
		var res Result

		log.Debugf("Processing chart: %s", chart.ChartURL())

		// If the state is not set to present add the namespace/release to the not rendered list
		if chart.State != config.StatePresent {
			absentCharts = append(absentCharts, chart.Namespace+"/"+chart.Release)
			continue
		}

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

		//
		// Template against the chart
		//
		cmdArgs = nil

		cmdArgs = append(cmdArgs, "template")

		// NAME
		cmdArgs = append(cmdArgs, chart.Release)

		// CHART
		cmdArgs = append(cmdArgs, chart.ChartURL())

		// Add the namespace if given
		if len(chart.Namespace) > 0 {
			cmdArgs = append(cmdArgs, "--namespace")
			cmdArgs = append(cmdArgs, chart.Namespace)
		}

		cmdArgs = append(cmdArgs, "--values")
		cmdArgs = append(cmdArgs, valuesFile)

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
			return fmt.Errorf("running helm template for release %s: %s: %w", chart.Release, res.Stderr, err)
		}

		fmt.Println(strings.TrimSpace(res.Stdout))
	}

	// Display output about the released that were not rendered
	if len(absentCharts) > 0 {
		log.Info("The following releases were set to absent and were not rendered.")
		for _, chart := range absentCharts {
			log.Infof("  %s", chart)
		}
	}

	return nil
}

func templateCmdPostRun() {
	log.Debug("Execution of the `template` command has completed.")
}
