package hostmodule

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	defaultHTTPTimeout  = 10 * time.Second
	maxHTTPResponseBody = 1 << 20 // 1MB
	maxHTTPRequestBody  = 1 << 20 // 1MB
	defaultUserAgent    = "go-scripts-host/1.0"
	maxRedirects        = 3
)

type HTTPOptions struct {
	AllowedDomains  []string
	Timeout         time.Duration
	MaxResponseBody int64
	DenyLoopback    bool
}

const EnvHTTPAllowedDomains = "SCRIPT_HTTP_ALLOWED_DOMAINS"

func HTTPAllowlistFromEnv() HTTPOptions {
	raw := strings.TrimSpace(os.Getenv(EnvHTTPAllowedDomains))
	opts := HTTPOptions{}
	if raw == "" {
		return opts
	}
	for _, d := range strings.Split(raw, ",") {
		if d = strings.TrimSpace(d); d != "" {
			opts.AllowedDomains = append(opts.AllowedDomains, d)
		}
	}
	return opts
}

func NormalizeHTTPOptions(o HTTPOptions) HTTPOptions {
	if o.Timeout <= 0 || o.Timeout > 30*time.Second {
		o.Timeout = defaultHTTPTimeout
	}
	if o.MaxResponseBody <= 0 || o.MaxResponseBody > maxHTTPResponseBody {
		o.MaxResponseBody = maxHTTPResponseBody
	}
	domains := make([]string, 0, len(o.AllowedDomains))
	for _, d := range o.AllowedDomains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d != "" {
			domains = append(domains, d)
		}
	}
	o.AllowedDomains = domains
	return o
}

func CheckAllowedURL(rawURL string, allowed []string, denyLoopback bool) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("only http/https schemes are allowed")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return errors.New("url host is empty")
	}
	if denyLoopback {
		for _, banned := range []string{"localhost", "127.0.0.1", "0.0.0.0", "::1", "169.254.169.254"} {
			if host == banned {
				return fmt.Errorf("host %q is not allowed", host)
			}
		}
	}

	for _, pattern := range allowed {
		if pattern == host {
			return nil
		}
		if strings.HasPrefix(pattern, "*.") {
			suffix := strings.TrimPrefix(pattern, "*")
			if strings.HasSuffix(host, suffix) && !strings.Contains(strings.TrimSuffix(host, suffix), ".") {
				return nil
			}
		}
	}
	return fmt.Errorf("host %q is not in the allowed domains list", host)
}

func HTTPRequest(ctx context.Context, client *http.Client, rawURL, method string, headers map[string]string, body string, maxBody int64) (map[string]any, error) {
	var bodyReader io.Reader
	if body != "" {
		if int64(len(body)) > maxHTTPRequestBody {
			return nil, errors.New("request body exceeds 1MB limit")
		}
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	for k, v := range headers {
		// 不允许脚本伪装/覆盖防护头
		lk := strings.ToLower(k)
		if lk == "host" || lk == "user-agent" || lk == "content-length" {
			continue
		}
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if int64(len(respBody)) > maxBody {
		return nil, errors.New("response body exceeds size limit")
	}

	respHeaders := make(map[string]string, len(resp.Header))
	for k, vs := range resp.Header {
		if len(vs) > 0 {
			respHeaders[strings.ToLower(k)] = vs[0]
		}
	}

	return map[string]any{
		"status":  resp.StatusCode,
		"body":    string(respBody),
		"headers": respHeaders,
	}, nil
}

func BuildHTTPClient(opts HTTPOptions) *http.Client {
	return &http.Client{
		Timeout: opts.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("too many redirects")
			}
			return CheckAllowedURL(req.URL.String(), opts.AllowedDomains, opts.DenyLoopback)
		},
	}
}

func errInvalid(msg string) error { return errors.New(msg) }

func ModuleHTTP(opts HTTPOptions) ModuleDef {
	normalized := NormalizeHTTPOptions(opts)
	client := BuildHTTPClient(normalized)

	blocked := func(rawURL string) error {
		if err := CheckAllowedURL(rawURL, normalized.AllowedDomains, normalized.DenyLoopback); err != nil {
			return errInvalid("http blocked: " + err.Error())
		}
		return nil
	}

	return ModuleDef{
		Name: "http",
		Funcs: map[string]any{
			"request": func(rawURL string, options ...map[string]any) (map[string]any, error) {
				if err := blocked(rawURL); err != nil {
					return nil, err
				}
				method := "GET"
				headers := map[string]string{}
				body := ""
				if len(options) > 0 && options[0] != nil {
					o := options[0]
					if m, ok := o["method"].(string); ok && m != "" {
						method = strings.ToUpper(m)
					}
					if h, ok := o["headers"].(map[string]any); ok {
						for k, v := range h {
							headers[k] = toStringValue(v)
						}
					}
					if b, ok := o["body"].(string); ok {
						body = b
					}
				}
				return HTTPRequest(context.Background(), client, rawURL, method, headers, body, normalized.MaxResponseBody)
			},
			"get": func(rawURL string) (map[string]any, error) {
				if err := blocked(rawURL); err != nil {
					return nil, err
				}
				return HTTPRequest(context.Background(), client, rawURL, http.MethodGet, nil, "", normalized.MaxResponseBody)
			},
			"post": func(rawURL, body string, contentType ...string) (map[string]any, error) {
				if err := blocked(rawURL); err != nil {
					return nil, err
				}
				ct := "application/json"
				if len(contentType) > 0 && contentType[0] != "" {
					ct = contentType[0]
				}
				return HTTPRequest(context.Background(), client, rawURL, http.MethodPost,
					map[string]string{"Content-Type": ct}, body, normalized.MaxResponseBody)
			},
		},
	}
}

func toStringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
