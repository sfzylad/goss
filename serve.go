package goss

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/goss-org/goss/outputs"
	"github.com/goss-org/goss/resource"
	"github.com/goss-org/goss/system"
	"github.com/goss-org/goss/util"
	"github.com/patrickmn/go-cache"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func Serve(c *util.Config) error {
	err := setLogLevel(c)
	if err != nil {
		return err
	}
	endpointsFile := c.EndpointsFile
	if endpointsFile != "" {
		cache := cache.New(c.Cache, 30*time.Second)
		_, err := newHealthHandlerWithEndpoints(c, cache)
		if err != nil {
			return err
		}

		log.Printf("[INFO] Starting to listen on: %s", c.ListenAddress)
		return http.ListenAndServe(c.ListenAddress, nil)
	}

	endpoint := c.Endpoint
	health, err := newHealthHandler(c)
	if err != nil {
		return err
	}
	http.Handle(endpoint, health)
	http.Handle("/metrics", promhttp.Handler())
	log.Printf("[INFO] Starting to listen on: %s", c.ListenAddress)
	return http.ListenAndServe(c.ListenAddress, nil)
}

func newHealthHandlerWithEndpoints(c *util.Config, cache *cache.Cache) ([]*healthHandler, error) {
	result := []*healthHandler{}
	color.NoColor = true

	health := &healthHandler{}

	ep, err := util.LoadEndpointsFile(c.EndpointsFile)
	if err != nil {
		return nil, err
	}

	// cache := cache.New(c.Cache, 30*time.Second)
	for i := range ep.Endpoints {
		localConfig := *c
		localConfig.Vars = ep.Endpoints[i].Vars
		localConfig.Endpoint = ep.Endpoints[i].Pattern
		localConfig.Spec = ep.Endpoints[i].Gossfile

		cfg, err := getGossConfig(localConfig.Vars, localConfig.VarsInline, localConfig.Spec)
		if err != nil {
			return nil, err
		}

		output, err := getOutputer(localConfig.NoColor, localConfig.OutputFormat)
		if err != nil {
			return nil, err
		}

		health = &healthHandler{
			c:             &localConfig,
			gossConfig:    *cfg,
			sys:           system.New(localConfig.PackageManager),
			outputer:      output,
			cache:         cache,
			gossMu:        &sync.Mutex{},
			maxConcurrent: localConfig.MaxConcurrent,
		}

		http.Handle(localConfig.Endpoint, health)
		result = append(result, health)
	}

	return result, nil
}

func newHealthHandler(c *util.Config) (*healthHandler, error) {
	color.NoColor = true
	cache := cache.New(c.Cache, 30*time.Second)

	cfg, err := getGossConfig(c.Vars, c.VarsInline, c.Spec)
	if err != nil {
		return nil, err
	}

	output, err := getOutputer(c.NoColor, c.OutputFormat)
	if err != nil {
		return nil, err
	}

	health := &healthHandler{
		c:             c,
		gossConfig:    *cfg,
		sys:           system.New(c.PackageManager),
		outputer:      output,
		cache:         cache,
		gossMu:        &sync.Mutex{},
		maxConcurrent: c.MaxConcurrent,
	}
	return health, nil
}

type res struct {
	body       bytes.Buffer
	statusCode int
}
type healthHandler struct {
	c *util.Config

	gossConfig    GossConfig
	sys           *system.System
	outputer      outputs.Outputer
	cache         *cache.Cache
	gossMu        *sync.Mutex
	maxConcurrent int
}

func (h healthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	outputFormat, outputer, err := h.negotiateResponseContentType(r)
	if err != nil {
		log.Printf("[DEBUG] Warn: Using process-level output-format. %s", err)
		outputFormat = h.c.OutputFormat
		outputer = h.outputer
	}
	negotiatedContentType := h.responseContentType(outputFormat)

	log.Printf("[TRACE] %v: requesting health probe", r.RemoteAddr)
	resp := h.processAndEnsureCached(negotiatedContentType, outputer)
	w.Header().Set(http.CanonicalHeaderKey("Content-Type"), negotiatedContentType) //nolint:gosimple
	w.WriteHeader(resp.statusCode)
	logBody := ""
	if resp.statusCode != http.StatusOK {
		logBody = " - " + resp.body.String()
	}
	resp.body.WriteTo(w)
	log.Printf("[DEBUG] %v: status %d%s", r.RemoteAddr, resp.statusCode, logBody)
}

func (h healthHandler) processAndEnsureCached(negotiatedContentType string, outputer outputs.Outputer) res {
	var tra [][]resource.TestResult
	cacheKey := h.c.Endpoint
	tmp, found := h.cache.Get(cacheKey)
	if found {
		log.Printf("[TRACE] Returning cached[%s].", cacheKey)
		tra = tmp.([][]resource.TestResult)
	} else {
		log.Printf("Stale cache[%s], running tests", cacheKey)
		h.sys = system.New(h.c.PackageManager)
		tra = h.validate()
		h.cache.SetDefault(cacheKey, tra)
	}
	trc := testResultArrayToChan(tra)
	return h.output(trc, outputer)
}

func (h healthHandler) output(trc <-chan []resource.TestResult, outputer outputs.Outputer) res {
	var b bytes.Buffer
	outputConfig := util.OutputConfig{
		FormatOptions: h.c.FormatOptions,
	}
	exitCode := outputer.Output(&b, trc, outputConfig)
	resp := res{
		body: b,
	}
	if exitCode == 0 {
		resp.statusCode = http.StatusOK
	} else {
		resp.statusCode = http.StatusServiceUnavailable
	}
	return resp
}

func (h healthHandler) validate() [][]resource.TestResult {
	h.sys = system.New(h.c.PackageManager)
	res := make([][]resource.TestResult, 0)
	tr := validate(h.sys, h.gossConfig, h.c.DisabledResourceTypes, h.maxConcurrent)
	for i := range tr {
		res = append(res, i)
	}
	return res
}

func testResultArrayToChan(tra [][]resource.TestResult) <-chan []resource.TestResult {
	c := make(chan []resource.TestResult)
	go func(c chan []resource.TestResult) {
		defer close(c)

		for _, i := range tra {
			c <- i
		}
	}(c)

	return c
}

const (
	// https://en.wikipedia.org/wiki/Media_type
	mediaTypePrefix = "application/vnd.goss-"
)

func (h healthHandler) negotiateResponseContentType(r *http.Request) (string, outputs.Outputer, error) {
	acceptHeader := r.Header[http.CanonicalHeaderKey("Accept")]
	var outputer outputs.Outputer
	outputName := ""
	for _, acceptCandidate := range acceptHeader {
		acceptCandidate = strings.TrimSpace(acceptCandidate)
		if strings.HasPrefix(acceptCandidate, mediaTypePrefix) {
			outputName = strings.TrimPrefix(acceptCandidate, mediaTypePrefix)
		} else if strings.EqualFold("application/json", acceptCandidate) || strings.EqualFold("text/json", acceptCandidate) {
			outputName = "json"
		} else {
			outputName = ""
		}
		var err error
		outputer, err = outputs.GetOutputer(outputName)
		if err != nil {
			continue
		}
	}
	if outputer == nil {
		return "", nil, fmt.Errorf("Accept header on request missing or invalid. Accept header: %v", acceptHeader)
	}

	return outputName, outputer, nil
}

func (h healthHandler) responseContentType(outputName string) string {
	if outputName == "json" {
		return "application/json"
	}
	if outputName == "prometheus" {
		return "text/plain; version=0.0.4"
	}

	return fmt.Sprintf("%s%s", mediaTypePrefix, outputName)
}
