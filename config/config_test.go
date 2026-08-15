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
	"testing"

	"github.com/spf13/viper"
)

func TestBooleanIsNotCoerced(t *testing.T) {
	viper.SetConfigFile("../testdata/demo.yml")
	viper.ReadInConfig()
	c, _ := LoadAndValidateFromViper()

	ingressConfig := c.Charts[0].Values["ingress"].(map[string]interface{})
	want := true
	got := ingressConfig["enabled"]
	if want != got {
		t.Errorf("want `ingress.enabled` to be type=%T value=%v, but got type=%T value=%v", want, want, got, got)
	}
}

func TestLoadAndValidateFromViper_PreservesValueKeyCase(t *testing.T) {
	viper.Reset()
	viper.SetConfigFile("../testdata/camel-case-values.yml")
	viper.ReadInConfig()

	c, err := LoadAndValidateFromViper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	values := c.Charts[0].Values

	// Helm values are case-sensitive: a camelCase top-level key must survive.
	if got, ok := values["nameOverride"]; !ok || got != "my-app" {
		keys := make([]string, 0, len(values))
		for k := range values {
			keys = append(keys, k)
		}
		t.Errorf("want values[nameOverride]=my-app, got value=%v present=%v; keys present=%v", got, ok, keys)
	}

	// A nested camelCase key must survive too.
	sa, ok := values["serviceAccount"].(map[string]interface{})
	if !ok {
		t.Fatalf("want values[serviceAccount] to be a map[string]interface{}, got %T", values["serviceAccount"])
	}
	if got := sa["name"]; got != "my-sa" {
		t.Errorf("want serviceAccount.name=my-sa, got %v", got)
	}
}

func TestLoadAndValidateFromViper_Unmarshallable(t *testing.T) {
	viper.SetConfigFile("../testdata/unmarshallable.yml")
	viper.ReadInConfig()

	_, err := LoadAndValidateFromViper()
	if err == nil {
		t.Errorf("want an error for unmarshallable data, but was nil")
	}
}

func TestLoadAndValidateFromViper_DefaultChartState(t *testing.T) {
	viper.SetConfigFile("../testdata/default-state.yml")
	viper.ReadInConfig()

	c, _ := LoadAndValidateFromViper()
	got := c.Charts[0].State
	want := "present"
	if got != want {
		t.Errorf("want state to be %s, but got %s", want, got)
	}
}

func TestLoadAndValidateFromViper_DefaultRepoState(t *testing.T) {
	viper.SetConfigFile("../testdata/default-state.yml")
	viper.ReadInConfig()

	c, _ := LoadAndValidateFromViper()
	got := c.Repositories[0].State
	want := "present"
	if got != want {
		t.Errorf("want state to be %s, but got %s", want, got)
	}
}
