package http

import (
	"net/http"
	"strings"
	"time"
)

type Option func(*configOptions)

type configOptions struct {
	baseURL    string
	prefix     string
	timeout    time.Duration
	headers    map[string]string
	httpClient *http.Client      // for tests; when non-nil the default client is overridden
	transport  http.RoundTripper // for tests; injected into the client
}

//

//

func WithBaseURL(baseURL string) Option {
	return func(o *configOptions) {

		if baseURL != "" && !strings.HasSuffix(baseURL, "/") {
			baseURL += "/"
		}
		o.baseURL = baseURL
	}
}

//

func WithPrefix(prefix string) Option {
	return func(o *configOptions) {
		prefix = strings.TrimPrefix(prefix, "/")
		o.prefix = prefix
	}
}

func WithTimeout(timeout time.Duration) Option {
	return func(o *configOptions) { o.timeout = timeout }
}

func WithHeader(key, value string) Option {
	return func(o *configOptions) {
		if o.headers == nil {
			o.headers = make(map[string]string)
		}
		o.headers[key] = value
	}
}

func WithHTTPClient(c *http.Client) Option {
	return func(o *configOptions) { o.httpClient = c }
}
