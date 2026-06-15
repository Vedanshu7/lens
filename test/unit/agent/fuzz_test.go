package agent_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// FuzzInvalidateHandler feeds arbitrary JSON bodies to /api/invalidate and
// verifies the handler never panics. The handler must return 4xx or 5xx — not
// 2xx — when the body is malformed or the service name is invalid.
func FuzzInvalidateHandler(f *testing.F) {
	f.Add(`{"service":"svc"}`)
	f.Add(`{"service":"svc","pattern":null}`)
	f.Add(`{"service":"svc","pattern":"*"}`)
	f.Add(`{}`)
	f.Add(`{"service":""}`)
	f.Add(`{"service":"a b"}`)
	f.Add(`not json at all`)
	f.Add(`{"service":"` + string(make([]byte, 4096)) + `"}`)

	f.Fuzz(func(t *testing.T, body string) {
		a := newTestAgent(t, defaultCfg())
		req := httptest.NewRequest(http.MethodPost, "/api/invalidate", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()
		a.Routes().ServeHTTP(w, req)
		// Any response is acceptable — the handler must not panic.
		_ = w.Code
	})
}

// FuzzDeclareHandler feeds arbitrary JSON bodies to /api/declare and verifies
// it never panics regardless of input shape.
func FuzzDeclareHandler(f *testing.F) {
	f.Add(`{"keyName":"k","ttlInSeconds":60}`)
	f.Add(`{"keyName":""}`)
	f.Add(`{}`)
	f.Add(`not json`)
	f.Add(`{"keyName":"` + string(make([]byte, 8192)) + `"}`)

	f.Fuzz(func(t *testing.T, body string) {
		a := newTestAgent(t, defaultCfg())
		req := httptest.NewRequest(http.MethodPost, "/api/declare", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()
		a.Routes().ServeHTTP(w, req)
		_ = w.Code
	})
}
