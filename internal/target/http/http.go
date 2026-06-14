// Package targethttp registers the "http" target provider, which communicates
// with the co-located app service over plain localhost HTTP.
// This provider is always compiled in (no build tag required).
package targethttp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Vedanshu7/lens/internal/target"
)

func init() {
	target.Register("http", func(cfg map[string]any) (target.TargetClient, error) {
		baseURL, _ := cfg["targetURL"].(string)
		if baseURL == "" {
			baseURL = "http://localhost:8080"
		}
		token, _ := cfg["token"].(string)
		return &httpClient{
			baseURL: strings.TrimRight(baseURL, "/"),
			token:   token,
			http:    &http.Client{Timeout: 5 * time.Second},
		}, nil
	})
}

type httpClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func (c *httpClient) Info(ctx context.Context) (target.TargetInfo, error) {
	resp, err := c.do(ctx, "GET", c.baseURL+"/internal/lens/info", "", nil)
	if err != nil {
		return target.TargetInfo{}, err
	}
	defer drain(resp)
	var info target.TargetInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return target.TargetInfo{}, fmt.Errorf("decode info: %w", err)
	}
	return info, nil
}

func (c *httpClient) Invalidate(ctx context.Context, payload []byte) error {
	resp, err := c.do(ctx, "POST", c.baseURL+"/internal/lens/invalidate", "application/json", payload)
	if err != nil {
		return err
	}
	defer drain(resp)
	if resp.StatusCode >= 500 {
		return fmt.Errorf("target returned %d", resp.StatusCode)
	}
	return nil
}

func (c *httpClient) Get(ctx context.Context, key string) ([]byte, error) {
	body, _ := json.Marshal(map[string]string{"key": key})
	resp, err := c.do(ctx, "POST", c.baseURL+"/internal/lens/get", "application/json", body)
	if err != nil {
		return nil, err
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("target returned %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func (c *httpClient) Keys(ctx context.Context, pattern, limit, offset string) ([]byte, error) {
	q := url.Values{}
	if pattern != "" {
		q.Set("pattern", pattern)
	}
	if limit != "" {
		q.Set("limit", limit)
	}
	if offset != "" {
		q.Set("offset", offset)
	}
	rawURL := c.baseURL + "/internal/lens/keys"
	if len(q) > 0 {
		rawURL += "?" + q.Encode()
	}
	resp, err := c.do(ctx, "GET", rawURL, "", nil)
	if err != nil {
		return nil, err
	}
	defer drain(resp)
	return io.ReadAll(resp.Body)
}

func (c *httpClient) Close() error { return nil }

func (c *httpClient) do(ctx context.Context, method, url, contentType string, body []byte) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.token != "" {
		req.Header.Set("x-lens-token", c.token)
	}
	return c.http.Do(req)
}

func drain(resp *http.Response) {
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()              //nolint:errcheck
}
