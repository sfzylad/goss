package util

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

/*
endpoints:
  - pattern: /svc
    gossfile: ./gossfile-svc.yaml
    vars: ./vars-svc.yaml
  - pattern: /healthz
    gossfile: ./gossfile.yaml
    vars: ./vars.yaml
*/

type EndpointsConfig struct {
	Endpoints []Endpoint `json:"endpoints"`
}

type Endpoint struct {
	Pattern  string `json:"pattern"`
	Gossfile string `json:"gossfile"`
	Vars     string `json:"vars"`
}

func LoadEndpointsFile(f string) (EndpointsConfig, error) {
	if f == "" {
		return EndpointsConfig{}, fmt.Errorf("no endpoiint file set")
	}
	b, err := os.ReadFile(f)
	if err != nil {
		return EndpointsConfig{}, fmt.Errorf("cannot load endpoints file: %s", err)
	}

	endpointsCfg := EndpointsConfig{}
	if err := yaml.Unmarshal(b, &endpointsCfg); err != nil {
		return EndpointsConfig{}, fmt.Errorf("cannot unmarshal endpoins file: %s", err)
	}

	return endpointsCfg, nil
}
