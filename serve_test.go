package goss

import (
	"bytes"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"text/template"
	"time"

	"github.com/goss-org/goss/util"
	"github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestServeWithNoContentNegotiation(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		outputFormat        string
		specFile            string
		expectedHTTPStatus  int
		expectedContentType string
	}{
		"passing-json": {
			outputFormat:        "json",
			specFile:            filepath.Join("testdata", "passing.goss.yaml"),
			expectedHTTPStatus:  http.StatusOK,
			expectedContentType: "application/json",
		},
		"failing-json": {
			outputFormat:        "json",
			specFile:            filepath.Join("testdata", "failing.goss.yaml"),
			expectedHTTPStatus:  http.StatusServiceUnavailable,
			expectedContentType: "application/json",
		},
		"failing-default-output": {
			outputFormat:        "rspecish",
			specFile:            filepath.Join("testdata", "failing.goss.yaml"),
			expectedHTTPStatus:  http.StatusServiceUnavailable,
			expectedContentType: "",
		},
	}
	for testName := range tests {
		tc := tests[testName]
		t.Run(testName, func(t *testing.T) {
			var logOutput bytes.Buffer
			log.SetOutput(&logOutput)

			config, err := util.NewConfig(
				util.WithSpecFile(tc.specFile),
				util.WithOutputFormat(tc.outputFormat),
			)
			require.NoError(t, err)

			hh, err := newHealthHandler(config)
			require.NoError(t, err)

			req := makeRequest(t, config, nil)
			rr := httptest.NewRecorder()

			handler := http.HandlerFunc(hh.ServeHTTP)

			handler.ServeHTTP(rr, req)

			t.Logf("testName %q log output:\n%s", testName, logOutput.String())
			assert.Equal(t, tc.expectedHTTPStatus, rr.Code)
			if tc.expectedContentType != "" {
				assert.Equal(t, tc.expectedContentType, rr.Result().Header.Get("Content-Type"))
			}
		})
	}
}

func TestServeWithNoContentNegotiationAndEndpointsFile(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		outputFormat        string
		endpointsFile       string
		expectedHTTPStatus  int
		expectedContentType string
	}{
		"passing-json": {
			outputFormat:        "json",
			endpointsFile:       makeMockEndpoints(t, 0, 2, true),
			expectedHTTPStatus:  http.StatusOK,
			expectedContentType: "application/json",
		},
		"failing-json": {
			outputFormat:        "json",
			endpointsFile:       makeMockEndpoints(t, 2, 2, false),
			expectedHTTPStatus:  http.StatusServiceUnavailable,
			expectedContentType: "application/json",
		},
		"failing-default-output": {
			outputFormat:        "rspecish",
			endpointsFile:       makeMockEndpoints(t, 4, 2, false),
			expectedHTTPStatus:  http.StatusServiceUnavailable,
			expectedContentType: "",
		},
	}
	for testName := range tests {
		tc := tests[testName]
		t.Run(testName, func(t *testing.T) {
			var logOutput bytes.Buffer
			log.SetOutput(&logOutput)

			config, err := util.NewConfig(
				util.WithEndpointsFile(tc.endpointsFile),
				util.WithOutputFormat(tc.outputFormat),
				util.WithEndpointsFile(tc.endpointsFile),
			)
			require.NoError(t, err)

			cache := cache.New(config.Cache, 30*time.Second)
			hh, err := newHealthHandlerWithEndpoints(config, cache)
			require.NoError(t, err)

			req := makeRequest(t, config, nil)
			rr := httptest.NewRecorder()

			for _, h := range hh {
				handler := http.HandlerFunc(h.ServeHTTP)
				handler.ServeHTTP(rr, req)

				t.Logf("testName %q log output:\n%s", testName, logOutput.String())
				assert.Equal(t, tc.expectedHTTPStatus, rr.Code)
				if tc.expectedContentType != "" {
					assert.Equal(t, tc.expectedContentType, rr.Result().Header.Get("Content-Type"))
				}
			}
		})
	}
}

func TestServeNegotiatingContent(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		acceptHeader        []string
		outputFormat        string
		specFile            string
		expectedHTTPStatus  int
		expectedContentType string
	}{
		"accept {blank} returns process-level format-option": {
			acceptHeader: []string{
				"",
			},
			outputFormat:        "structured",
			specFile:            filepath.Join("testdata", "passing.goss.yaml"),
			expectedHTTPStatus:  http.StatusOK,
			expectedContentType: "application/vnd.goss-structured",
		},
		"accept application/json": {
			acceptHeader: []string{
				"application/json",
			},
			outputFormat:        "structured",
			specFile:            filepath.Join("testdata", "passing.goss.yaml"),
			expectedHTTPStatus:  http.StatusOK,
			expectedContentType: "application/json",
		},
		"accept text/json translates to application/json": {
			acceptHeader: []string{
				"text/json",
			},
			outputFormat:        "structured",
			specFile:            filepath.Join("testdata", "passing.goss.yaml"),
			expectedHTTPStatus:  http.StatusOK,
			expectedContentType: "application/json",
		},
		"when accept is application/vnd.goss-json, return more widely known application/json": {
			acceptHeader: []string{
				"application/vnd.goss-json",
			},
			outputFormat:        "structured",
			specFile:            filepath.Join("testdata", "passing.goss.yaml"),
			expectedHTTPStatus:  http.StatusOK,
			expectedContentType: "application/json",
		},
		"accept prometheus": {
			acceptHeader: []string{
				"text/plain; version=0.0.4",
			},
			outputFormat:        "prometheus",
			specFile:            filepath.Join("testdata", "passing.goss.yaml"),
			expectedHTTPStatus:  http.StatusOK,
			expectedContentType: "text/plain; version=0.0.4",
		},
		"accept header contains vendor-specific output format different from process-level": {
			acceptHeader: []string{
				"application/vnd.goss-rspecish",
			},
			outputFormat:        "structured",
			specFile:            filepath.Join("testdata", "passing.goss.yaml"),
			expectedHTTPStatus:  http.StatusOK,
			expectedContentType: "application/vnd.goss-rspecish",
		},
		"accept header contains nonsense": {
			acceptHeader: []string{
				"application/vnd.goss-nonexistent",
			},
			outputFormat:        "structured",
			specFile:            filepath.Join("testdata", "passing.goss.yaml"),
			expectedHTTPStatus:  http.StatusOK,
			expectedContentType: "application/vnd.goss-structured",
		},
		"accept header contains nonsense then valid": {
			acceptHeader: []string{
				"application/vnd.goss-nonexistent",
				"application/json",
			},
			outputFormat:        "structured",
			specFile:            filepath.Join("testdata", "passing.goss.yaml"),
			expectedHTTPStatus:  http.StatusOK,
			expectedContentType: "application/json",
		},
	}
	for testName := range tests {
		tc := tests[testName]
		t.Run(testName, func(t *testing.T) {
			var logOutput bytes.Buffer
			log.SetOutput(&logOutput)

			config, err := util.NewConfig(
				util.WithSpecFile(tc.specFile),
				util.WithOutputFormat(tc.outputFormat),
			)
			require.NoError(t, err)

			hh, err := newHealthHandler(config)
			require.NoError(t, err)

			req := makeRequest(t, config, map[string][]string{
				"accept": tc.acceptHeader,
			})
			rr := httptest.NewRecorder()

			handler := http.HandlerFunc(hh.ServeHTTP)

			handler.ServeHTTP(rr, req)

			t.Logf("testName %q log output:\n%s", testName, logOutput.String())
			assert.Equal(t, tc.expectedHTTPStatus, rr.Code)
			if tc.expectedContentType != "" {
				assert.Equal(t, tc.expectedContentType, rr.Result().Header.Get("Content-Type"))
			}
		})
	}
}

func TestServeNegotiatingContentAndEndpointsFile(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		acceptHeader        []string
		outputFormat        string
		endpointsFile       string
		expectedHTTPStatus  int
		expectedContentType string
	}{
		"accept {blank} returns process-level format-option": {
			acceptHeader: []string{
				"",
			},
			outputFormat:        "structured",
			endpointsFile:       makeMockEndpoints(t, 0, 2, true),
			expectedHTTPStatus:  http.StatusOK,
			expectedContentType: "application/vnd.goss-structured",
		},
		"accept application/json": {
			acceptHeader: []string{
				"application/json",
			},
			outputFormat:        "structured",
			endpointsFile:       makeMockEndpoints(t, 2, 2, true),
			expectedHTTPStatus:  http.StatusOK,
			expectedContentType: "application/json",
		},
		"accept text/json translates to application/json": {
			acceptHeader: []string{
				"text/json",
			},
			outputFormat:        "structured",
			endpointsFile:       makeMockEndpoints(t, 4, 2, true),
			expectedHTTPStatus:  http.StatusOK,
			expectedContentType: "application/json",
		},
		"when accept is application/vnd.goss-json, return more widely known application/json": {
			acceptHeader: []string{
				"application/vnd.goss-json",
			},
			outputFormat:        "structured",
			endpointsFile:       makeMockEndpoints(t, 6, 2, true),
			expectedHTTPStatus:  http.StatusOK,
			expectedContentType: "application/json",
		},
		"accept prometheus": {
			acceptHeader: []string{
				"text/plain; version=0.0.4",
			},
			outputFormat:        "prometheus",
			endpointsFile:       makeMockEndpoints(t, 8, 2, true),
			expectedHTTPStatus:  http.StatusOK,
			expectedContentType: "text/plain; version=0.0.4",
		},
		"accept header contains vendor-specific output format different from process-level": {
			acceptHeader: []string{
				"application/vnd.goss-rspecish",
			},
			outputFormat:        "structured",
			endpointsFile:       makeMockEndpoints(t, 10, 2, true),
			expectedHTTPStatus:  http.StatusOK,
			expectedContentType: "application/vnd.goss-rspecish",
		},
		"accept header contains nonsense": {
			acceptHeader: []string{
				"application/vnd.goss-nonexistent",
			},
			outputFormat:        "structured",
			endpointsFile:       makeMockEndpoints(t, 12, 2, true),
			expectedHTTPStatus:  http.StatusOK,
			expectedContentType: "application/vnd.goss-structured",
		},
		"accept header contains nonsense then valid": {
			acceptHeader: []string{
				"application/vnd.goss-nonexistent",
				"application/json",
			},
			outputFormat:        "structured",
			endpointsFile:       makeMockEndpoints(t, 14, 2, true),
			expectedHTTPStatus:  http.StatusOK,
			expectedContentType: "application/json",
		},
	}
	for testName := range tests {
		tc := tests[testName]
		t.Run(testName, func(t *testing.T) {
			var logOutput bytes.Buffer
			log.SetOutput(&logOutput)

			config, err := util.NewConfig(
				util.WithEndpointsFile(tc.endpointsFile),
				util.WithOutputFormat(tc.outputFormat),
			)
			require.NoError(t, err)

			cache := cache.New(config.Cache, 30*time.Second)

			hh, err := newHealthHandlerWithEndpoints(config, cache)
			require.NoError(t, err)

			for _, h := range hh {
				req := makeRequest(t, config, map[string][]string{
					"accept": tc.acceptHeader,
				})
				rr := httptest.NewRecorder()

				handler := http.HandlerFunc(h.ServeHTTP)

				handler.ServeHTTP(rr, req)

				t.Logf("testName %q log output:\n%s", testName, logOutput.String())
				assert.Equal(t, tc.expectedHTTPStatus, rr.Code)
				if tc.expectedContentType != "" {
					assert.Equal(t, tc.expectedContentType, rr.Result().Header.Get("Content-Type"))
				}
			}
		})
	}
}

func TestServeCacheWithNoContentNegotiation(t *testing.T) {
	var logOutput bytes.Buffer
	log.SetOutput(&logOutput)
	const cache = time.Duration(time.Millisecond * 100)
	config, err := util.NewConfig(
		util.WithSpecFile(filepath.Join("testdata", "passing.goss.yaml")),
		util.WithCache(cache),
	)
	require.NoError(t, err)

	hh, err := newHealthHandler(config)
	require.NoError(t, err)

	req := makeRequest(t, config, nil)
	rr := httptest.NewRecorder()

	handler := http.HandlerFunc(hh.ServeHTTP)

	t.Run("fresh cache", func(t *testing.T) {
		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Result().StatusCode)
		assert.Contains(t, logOutput.String(), "Stale cache")
		t.Log(logOutput.String())
		logOutput.Reset()
	})

	t.Run("immediately re-request, cache should be warm", func(t *testing.T) {
		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Result().StatusCode)
		assert.NotContains(t, logOutput.String(), "Stale cache")
		t.Log(logOutput.String())
		logOutput.Reset()
	})

	t.Run("allow cache to expire, cache should be cold", func(t *testing.T) {
		time.Sleep(cache + 5*time.Millisecond)
		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Result().StatusCode)
		assert.Contains(t, logOutput.String(), "Stale cache")
		t.Log(logOutput.String())
		logOutput.Reset()
	})
}

func TestServeCacheWithNoContentNegotiationAndEndpointsFile(t *testing.T) {
	endpointsFile := makeMockEndpoints(t, 0, 2, true)
	var logOutput bytes.Buffer
	log.SetOutput(&logOutput)
	const cacheTTL = time.Duration(time.Millisecond * 100)
	config, err := util.NewConfig(
		util.WithEndpointsFile(endpointsFile),
		util.WithCache(cacheTTL),
	)
	require.NoError(t, err)

	cache := cache.New(config.Cache, 30*time.Second)

	hh, err := newHealthHandlerWithEndpoints(config, cache)
	require.NoError(t, err)

	for _, h := range hh {
		req := makeRequest(t, config, nil)
		rr := httptest.NewRecorder()

		handler := http.HandlerFunc(h.ServeHTTP)

		t.Run("fresh cache", func(t *testing.T) {
			handler.ServeHTTP(rr, req)

			assert.Equal(t, http.StatusOK, rr.Result().StatusCode)
			assert.Contains(t, logOutput.String(), "Stale cache")
			t.Log(logOutput.String())
			logOutput.Reset()
		})

		t.Run("immediately re-request, cache should be warm", func(t *testing.T) {
			handler.ServeHTTP(rr, req)

			assert.Equal(t, http.StatusOK, rr.Result().StatusCode)
			assert.NotContains(t, logOutput.String(), "Stale cache")
			t.Log(logOutput.String())
			logOutput.Reset()
		})

		t.Run("allow cache to expire, cache should be cold", func(t *testing.T) {
			time.Sleep(cacheTTL + 5*time.Millisecond)
			handler.ServeHTTP(rr, req)

			assert.Equal(t, http.StatusOK, rr.Result().StatusCode)
			assert.Contains(t, logOutput.String(), "Stale cache")
			t.Log(logOutput.String())
			logOutput.Reset()
		})
	}
}

func TestServeCacheNegotiatingContent(t *testing.T) {
	var logOutput bytes.Buffer
	log.SetOutput(&logOutput)
	const cache = time.Duration(time.Millisecond * 100)
	config, err := util.NewConfig(
		util.WithSpecFile(filepath.Join("testdata", "passing.goss.yaml")),
		util.WithCache(cache),
		util.WithOutputFormat("structured"),
	)
	require.NoError(t, err)

	hh, err := newHealthHandler(config)
	require.NoError(t, err)

	rr := httptest.NewRecorder()

	handler := http.HandlerFunc(hh.ServeHTTP)

	t.Run("fresh cache", func(t *testing.T) {
		req := makeRequest(t, config, map[string][]string{
			"accept": {"application/json"},
		})
		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Result().StatusCode)
		assert.Contains(t, logOutput.String(), "Stale cache")
		t.Log(logOutput.String())
		logOutput.Reset()
	})

	t.Run("immediately re-request, cache should be warm", func(t *testing.T) {
		req := makeRequest(t, config, map[string][]string{
			"accept": {"application/json"},
		})
		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Result().StatusCode)
		assert.NotContains(t, logOutput.String(), "Stale cache")
		t.Log(logOutput.String())
		logOutput.Reset()
	})

	t.Run("immediately re-request but different accept header, cache should be warm", func(t *testing.T) {
		req := makeRequest(t, config, map[string][]string{
			"accept": {"application/vnd.goss-rspecish"},
		})
		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Result().StatusCode)
		assert.NotContains(t, logOutput.String(), "Stale cache")
		t.Log(logOutput.String())
		logOutput.Reset()
	})

	t.Run("allow cache to expire, cache should be cold", func(t *testing.T) {
		time.Sleep(cache + 5*time.Millisecond)
		req := makeRequest(t, config, map[string][]string{
			"accept": {"application/json"},
		})
		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Result().StatusCode)
		assert.Contains(t, logOutput.String(), "Stale cache")
		t.Log(logOutput.String())
		logOutput.Reset()
	})
}

func TestServeCacheNegotiatingContentAndEndpointsFile(t *testing.T) {
	endpointsFile := makeMockEndpoints(t, 0, 2, true)
	var logOutput bytes.Buffer
	log.SetOutput(&logOutput)
	const cacheTTL = time.Duration(time.Millisecond * 100)
	config, err := util.NewConfig(
		util.WithEndpointsFile(endpointsFile),
		util.WithCache(cacheTTL),
		util.WithOutputFormat("structured"),
	)
	require.NoError(t, err)

	cache := cache.New(config.Cache, 30*time.Second)

	hh, err := newHealthHandlerWithEndpoints(config, cache)
	require.NoError(t, err)

	for _, h := range hh {
		rr := httptest.NewRecorder()

		handler := http.HandlerFunc(h.ServeHTTP)

		t.Run("fresh cache", func(t *testing.T) {
			req := makeRequest(t, config, map[string][]string{
				"accept": {"application/json"},
			})
			handler.ServeHTTP(rr, req)

			assert.Equal(t, http.StatusOK, rr.Result().StatusCode)
			assert.Contains(t, logOutput.String(), "Stale cache")
			t.Log(logOutput.String())
			logOutput.Reset()
		})

		t.Run("immediately re-request, cache should be warm", func(t *testing.T) {
			req := makeRequest(t, config, map[string][]string{
				"accept": {"application/json"},
			})
			handler.ServeHTTP(rr, req)

			assert.Equal(t, http.StatusOK, rr.Result().StatusCode)
			assert.NotContains(t, logOutput.String(), "Stale cache")
			t.Log(logOutput.String())
			logOutput.Reset()
		})

		t.Run("immediately re-request but different accept header, cache should be warm", func(t *testing.T) {
			req := makeRequest(t, config, map[string][]string{
				"accept": {"application/vnd.goss-rspecish"},
			})
			handler.ServeHTTP(rr, req)

			assert.Equal(t, http.StatusOK, rr.Result().StatusCode)
			assert.NotContains(t, logOutput.String(), "Stale cache")
			t.Log(logOutput.String())
			logOutput.Reset()
		})

		t.Run("allow cache to expire, cache should be cold", func(t *testing.T) {
			time.Sleep(cacheTTL + 5*time.Millisecond)
			req := makeRequest(t, config, map[string][]string{
				"accept": {"application/json"},
			})
			handler.ServeHTTP(rr, req)

			assert.Equal(t, http.StatusOK, rr.Result().StatusCode)
			assert.Contains(t, logOutput.String(), "Stale cache")
			t.Log(logOutput.String())
			logOutput.Reset()
		})
	}
}

func makeRequest(t *testing.T, config *util.Config, headers map[string][]string) *http.Request {
	req, err := http.NewRequest("GET", config.Endpoint, nil)
	require.NoError(t, err)
	for header, vals := range headers {
		for _, v := range vals {
			req.Header.Add(header, v)
		}
	}
	return req
}

func makeMockEndpoints(t *testing.T, baseN, ceilingN int, passing bool) string {
	passingContent := `
---
command:
  hello world:
    exit-status: 0
    exec: "echo svc{{.SvcN}} - hello world"
    stdout:
      - svc{{.SvcN}} - hello world
    stderr: []
    timeout: 10000
`

	failingContent := `
---
command:
  hello world:
    exit-status: 0
    exec: "echo foobar"
    stdout:
      - svc{{.SvcN}} - hello world
    stderr: []
    timeout: 10000
`
	endpoints := util.EndpointsConfig{
		Endpoints: []util.Endpoint{},
	}

	endpointFile, err := os.CreateTemp("", fmt.Sprintf("endpoint.%s.yaml", generateRandomString(5)))
	require.NoError(t, err)
	defer endpointFile.Close()

	t.Logf("endpointFile: %s", endpointFile.Name())

	for i := baseN; i < baseN+ceilingN; i++ {
		randomEndpoint := fmt.Sprintf("/svc/%s", generateRandomString(5))
		gossFile, err := os.CreateTemp("", fmt.Sprintf("%s-*.yaml", filepath.Base(randomEndpoint)))
		defer gossFile.Close()
		require.NoError(t, err)

		t.Logf("randomEndpoint: %s", randomEndpoint)
		t.Logf("gossFile: %s", gossFile.Name())

		emptyVarsFile, err := os.CreateTemp("", "goss-tmp-vars-*.yaml")
		require.NoError(t, err)
		err = emptyVarsFile.Close()
		require.NoError(t, err)

		t.Logf("emptyVarsFile: %s", emptyVarsFile.Name())

		endpoints.Endpoints = append(endpoints.Endpoints, util.Endpoint{
			Pattern:  randomEndpoint,
			Gossfile: gossFile.Name(),
			Vars:     emptyVarsFile.Name(),
		})

		input := struct{ SvcN string }{strconv.Itoa(i)}

		content := failingContent
		if passing {
			content = passingContent
		}

		tmpl, err := template.New(gossFile.Name()).Parse(content)
		err = tmpl.Execute(gossFile, input)
		require.NoError(t, err)

	}
	endpointsContent, err := yaml.Marshal(endpoints)
	require.NoError(t, err)

	_, err = endpointFile.Write(endpointsContent)
	require.NoError(t, err)

	err = endpointFile.Close()
	require.NoError(t, err)

	return endpointFile.Name()
}

func generateRandomString(n int) string {
	charset := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)

	for i := range b {
		b[i] = charset[rand.IntN(len(charset))]
	}
	return string(b)
}
