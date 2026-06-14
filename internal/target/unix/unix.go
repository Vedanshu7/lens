// Package targetunix registers the "unix" target provider, which communicates
// with the co-located app service over a Unix domain socket using the same
// HTTP contract as the default http provider. This eliminates TCP stack
// overhead for the sidecar-to-service last-mile call.
package targetunix

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Vedanshu7/lens/internal/target"
)

func init() {
	target.Register("unix", func(cfg map[string]any) (target.TargetClient, error) {
		socketPath, _ := cfg["socketPath"].(string)
		if socketPath == "" {
			return nil, fmt.Errorf("target unix: socketPath is required")
		}
		token, _ := cfg["token"].(string)
		transport := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		}
		return &unixClient{
			token: token,
			http:  &http.Client{Transport: transport, Timeout: 5 * time.Second},
		}, nil
	})
}

type unixClient struct {
	token string
	http  *http.Client
}

// baseURL uses a dummy host — Unix socket ignores the host component.
const baseURL = "http://localhost"

func (c *unixClient) Info(ctx context.Context) (target.TargetInfo, error) {
	resp, err := c.do(ctx, "GET", baseURL+"/internal/lens/info", "", nil)
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

func (c *unixClient) Invalidate(ctx context.Context, payload []byte) error {
	resp, err := c.do(ctx, "POST", baseURL+"/internal/lens/invalidate", "application/json", payload)
	if err != nil {
		return err
	}
	defer drain(resp)
	if resp.StatusCode >= 500 {
		return fmt.Errorf("target returned %d", resp.StatusCode)
	}
	return nil
}

func (c *unixClient) Get(ctx context.Context, key string) ([]byte, error) {
	body, _ := json.Marshal(map[string]string{"key": key})
	resp, err := c.do(ctx, "POST", baseURL+"/internal/lens/get", "application/json", body)
	if err != nil {
		return nil, err
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("target returned %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func (c *unixClient) Keys(ctx context.Context, pattern, limit, offset string) ([]byte, error) {
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
	rawURL := baseURL + "/internal/lens/keys"
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

func (c *unixClient) Close() error { return nil }

func (c *unixClient) do(ctx context.Context, method, rawURL, contentType string, body []byte) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
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
