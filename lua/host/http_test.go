package host

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	hostmodule "github.com/liujitcn/go-scripts/hostmodule"
	lua "github.com/yuin/gopher-lua"
)

func setupHTTPEngine(t *testing.T, allowed []string) (*lua.LState, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Header().Set("X-Probe", "yes")
			_, _ = w.Write([]byte(`{"hello":"world"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ts.Close)

	L := lua.NewState()
	L.PreloadModule("kratos_http", LoaderHTTP(hostmodule.HTTPOptions{AllowedDomains: allowed, DenyLoopback: false}))
	return L, ts
}

func runLua(t *testing.T, L *lua.LState, code string) error {
	t.Helper()
	if err := L.DoString(code); err != nil {
		return err
	}
	return nil
}

func TestHTTP_AllowedRequest(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	}))
	defer ts.Close()

	u, _ := url.Parse(ts.URL)
	host := u.Hostname()
	L := lua.NewState()
	defer L.Close()
	L.PreloadModule("kratos_http", LoaderHTTP(hostmodule.HTTPOptions{AllowedDomains: []string{host}, DenyLoopback: false}))

	err := runLua(t, L, `
		local http = require "kratos_http"
		local resp = http.get("`+ts.URL+`/ok")
		assert(resp.status == 200, "status=" .. tostring(resp.status))
		assert(resp.body == '{"hello":"world"}', "body=" .. tostring(resp.body))
	`)
	if err != nil {
		t.Fatalf("allowed request failed: %v", err)
	}
}

func TestHTTP_BlockedByWhitelist(t *testing.T) {
	L, ts := setupHTTPEngine(t, []string{"hooks.example.com"})
	defer ts.Close()
	defer L.Close()

	err := runLua(t, L, `
		local http = require "kratos_http"
		http.get("`+ts.URL+`/ok")
	`)
	if err == nil {
		t.Fatal("expected whitelist block, got nil error")
	}
	if !strings.Contains(err.Error(), "not in the allowed domains list") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHTTP_EmptyWhitelistFailsClosed(t *testing.T) {
	L, ts := setupHTTPEngine(t, nil)
	defer ts.Close()
	defer L.Close()

	err := runLua(t, L, `
		local http = require "kratos_http"
		http.get("`+ts.URL+`/ok")
	`)
	if err == nil {
		t.Fatal("expected fail-closed block, got nil error")
	}
}

func TestHTTP_LocalhostAlwaysBlocked(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	L.PreloadModule("kratos_http", LoaderHTTP(hostmodule.HTTPOptions{AllowedDomains: []string{"localhost", "127.0.0.1"}}))

	err := runLua(t, L, `
		local http = require "kratos_http"
		http.get("http://127.0.0.1:1/x")
	`)
	if err == nil {
		t.Fatal("expected localhost block, got nil error")
	}
}

func TestHTTP_PostBody(t *testing.T) {
	var gotBody, gotCT string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		gotCT = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	u, _ := url.Parse(ts.URL)
	host := u.Hostname()
	L := lua.NewState()
	defer L.Close()
	L.PreloadModule("kratos_http", LoaderHTTP(hostmodule.HTTPOptions{AllowedDomains: []string{host}, DenyLoopback: false}))

	err := runLua(t, L, `
		local http = require "kratos_http"
		local resp = http.post("`+ts.URL+`", '{"a":1}', "application/json")
		assert(resp.status == 200, "status=" .. tostring(resp.status))
	`)
	if err != nil {
		t.Fatalf("post failed: %v", err)
	}
	if gotBody != `{"a":1}` {
		t.Errorf("body not delivered: %q", gotBody)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type not delivered: %q", gotCT)
	}
}
