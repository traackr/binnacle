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

package config

import (
	"fmt"
	"os"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// StatePresent represents the present state
const StatePresent = "present"

// BinnacleConfig definition
type BinnacleConfig struct {
	Charts       []ChartConfig `mapstructure:"charts"`
	ConfigFile   string
	Context      string             `mapstructure:"kube-context"`
	LogLevel     string             `mapstructure:"loglevel"`
	Release      string             `mapstructure:"release"`
	Repositories []RepositoryConfig `mapstructure:"repositories"`
}

// LoadAndValidateFromViper creates a BinnacleConfig object from Viper
func LoadAndValidateFromViper() (*BinnacleConfig, error) {
	var config BinnacleConfig

	if err := viper.UnmarshalExact(&config); err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	config.ConfigFile = viper.ConfigFileUsed()

	// viper lowercases every key, which corrupts case-sensitive Helm value keys
	// (e.g. nameOverride, serviceAccount). Re-read the raw `values:` blocks with
	// yaml.v3 (case-preserving) and use those instead of viper's decoded maps.
	if err := reparseChartValues(&config); err != nil {
		return nil, err
	}

	// Set general defaults
	if len(config.Context) == 0 {
		config.Context = "default"
	}

	// Set defaults for charts
	for idx := range config.Charts {
		chart := &config.Charts[idx]

		if len(chart.Repo) == 0 {
			chart.Repo = ""
		}

		if len(chart.State) == 0 {
			chart.State = StatePresent
		}

		for k, v := range chart.Values {
			chart.Values[k] = cleanupMapValue(v)
		}
	}

	// Set defaults for repos
	for idx, repo := range config.Repositories {
		if len(repo.State) == 0 {
			config.Repositories[idx].State = StatePresent
		}
	}

	if err := validateConfig(&config); err != nil {
		return nil, err
	}

	return &config, nil
}

// reparseChartValues re-reads each chart's `values` block directly from the
// config file with yaml.v3 so case-sensitive Helm keys survive viper's key
// lowercasing. Charts are matched by document order, which viper and yaml.v3
// both preserve.
func reparseChartValues(config *BinnacleConfig) error {
	if config.ConfigFile == "" {
		return nil
	}

	data, err := os.ReadFile(config.ConfigFile)
	if err != nil {
		return fmt.Errorf("re-reading config file for chart values: %w", err)
	}

	var raw struct {
		Charts []struct {
			Values map[string]any `yaml:"values"`
		} `yaml:"charts"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parsing chart values: %w", err)
	}

	for i := range config.Charts {
		if i < len(raw.Charts) {
			config.Charts[i].Values = raw.Charts[i].Values
		}
	}

	return nil
}

func cleanupInterfaceArray(in []interface{}) []interface{} {
	res := make([]interface{}, len(in))
	for i, v := range in {
		res[i] = cleanupMapValue(v)
	}
	return res
}

func cleanupInterfaceMap(in map[interface{}]interface{}) map[string]interface{} {
	res := make(map[string]interface{})
	for k, v := range in {
		res[fmt.Sprintf("%v", k)] = cleanupMapValue(v)
	}
	return res
}

// In order to marshal to JSON, map keys must be Strings even though YAML allows other types
// Recursively walk through this value to transform map[interface{}]interface{} into map[string]interface{}
//
// See: https://github.com/go-yaml/yaml/issues/139 (supposedly a fix will be available in v3 of go-yaml)
func cleanupMapValue(v interface{}) interface{} {
	switch v := v.(type) {
	case []interface{}:
		return cleanupInterfaceArray(v)
	case map[interface{}]interface{}:
		return cleanupInterfaceMap(v)
	default:
		return v
	}
}

func validateConfig(c *BinnacleConfig) error {
	return nil
}
