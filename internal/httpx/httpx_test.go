package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestDoAndRetryAfter 验证响应体读取与 Retry-After 解析。
func TestDoAndRetryAfter(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(429)
		w.Write([]byte("slow down"))
	}))
	defer s.Close()
	req, _ := http.NewRequest("GET", s.URL, nil)
	r, err := Do(context.Background(), NewClient(5*time.Second), req)
	if err != nil || r.Status != 429 || string(r.Body) != "slow down" || r.RetryAfter != 7*time.Second {
		t.Errorf("%+v %v", r, err)
	}
}

// TestIsHTMLBlock 验证拦截页识别。
func TestIsHTMLBlock(t *testing.T) {
	if !IsHTMLBlock([]byte("  <html>")) || IsHTMLBlock([]byte(`{"a":1}`)) || IsHTMLBlock(nil) {
		t.Error("IsHTMLBlock")
	}
	if !strings.Contains(BrowserUA, "Safari") {
		t.Error("UA")
	}
}

// TestDoTimeout 验证超时返回错误。
func TestDoTimeout(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(time.Second) }))
	defer s.Close()
	req, _ := http.NewRequest("GET", s.URL, nil)
	if _, err := Do(context.Background(), NewClient(100*time.Millisecond), req); err == nil {
		t.Error("应超时")
	}
}
