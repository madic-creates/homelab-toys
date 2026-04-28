package prom

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestQuery_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("query"); got != `up{job="prometheus"}` {
			t.Errorf("query = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": "success",
			"data": {
				"resultType": "vector",
				"result": [
					{"metric":{"job":"prometheus"},"value":[1714000000,"1"]},
					{"metric":{"job":"node"},"value":[1714000000,"0"]}
				]
			}
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	res, err := c.Query(context.Background(), `up{job="prometheus"}`)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("len(result) = %d, want 2", len(res))
	}
	if res[0].Metric["job"] != "prometheus" || res[0].Value != "1" {
		t.Errorf("res[0] = %+v", res[0])
	}
}

func TestQuery_PrometheusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"unexpected token"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	_, err := c.Query(context.Background(), "broken{")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestQuery_HTTPNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	_, err := c.Query(context.Background(), "up")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestQuery_URLEncodes(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	if _, err := c.Query(context.Background(), `count by (state) (longhorn_volume_robustness == 1)`); err != nil {
		t.Fatalf("Query: %v", err)
	}
	parsed, err := url.ParseQuery(got)
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}
	if parsed.Get("query") != `count by (state) (longhorn_volume_robustness == 1)` {
		t.Errorf("query encoded as %q", parsed.Get("query"))
	}
}

func TestQuery_PrometheusError_With4xxStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"parse error at char 5"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	_, err := c.Query(context.Background(), "broken{")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	for _, want := range []string{"422", "bad_data", "parse error at char 5"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err.Error(), want)
		}
	}
}
