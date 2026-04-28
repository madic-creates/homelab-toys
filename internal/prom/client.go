// Package prom is a tiny wrapper around the Prometheus instant-query API.
// It does not pull in github.com/prometheus/common/model or the upstream
// API client — both of those add a lot of surface area for the two queries
// we actually run.
package prom

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// maxResponseBytes bounds the size of a Prometheus response body. A
// vector result with thousands of pods is still small (well under 1 MiB);
// the 16 MiB ceiling tolerates pathological queries while preventing a
// misbehaving server from streaming an unbounded payload into the decoder.
const maxResponseBytes = 16 << 20 // 16 MiB

// Sample is one element of the `data.result` vector. Value is the string
// form Prometheus emits — callers parse with strconv.ParseFloat or read
// it as-is for "is this exactly 1?" checks.
type Sample struct {
	Metric map[string]string
	Value  string
}

type Client struct {
	baseURL string
	hc      *http.Client
}

func NewClient(baseURL string, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), hc: hc}
}

// Query runs a Prometheus instant query and returns the result vector.
// Range queries are deliberately unsupported — cluster-tv has no use for them.
func (c *Client) Query(ctx context.Context, q string) ([]Sample, error) {
	v := url.Values{}
	v.Set("query", q)
	u := c.baseURL + "/api/v1/query?" + v.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prom request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var raw struct {
		Status    string `json:"status"`
		ErrorType string `json:"errorType,omitempty"`
		Error     string `json:"error,omitempty"`
		Data      struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  [2]any            `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	// Decode whatever the server sent. Prometheus returns the same envelope
	// for both success and error (e.g. 422 bad_data carries errorType+error
	// in the body), so a single decode handles both paths.
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&raw); err != nil {
		// If the body was unparseable AND the status was non-OK, prefer to
		// surface the status — the body is probably an HTML error page from
		// a misconfigured proxy and not useful in the error.
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("prom: status %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("prom: decode: %w", err)
	}
	if raw.Status != "success" {
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("prom: status %d: %s: %s", resp.StatusCode, raw.ErrorType, raw.Error)
		}
		return nil, fmt.Errorf("prom: %s: %s", raw.ErrorType, raw.Error)
	}

	out := make([]Sample, 0, len(raw.Data.Result))
	for _, r := range raw.Data.Result {
		val, _ := r.Value[1].(string)
		out = append(out, Sample{Metric: r.Metric, Value: val})
	}
	return out, nil
}
