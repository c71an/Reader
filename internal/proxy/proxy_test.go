package proxy

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

func TestGetRefererForURL(t *testing.T) {
	p := NewImageProxy()

	tests := []struct {
		url      string
		expected string
	}{
		{"https://wx1.sinaimg.cn/large/006abcdef.jpg", "https://weibo.com/"},
		{"https://sns-webpic-qc.xhscdn.com/notes_uhdr/123.jpg", "https://www.xiaohongshu.com/"},
		{"https://pic1.zhimg.com/v2-abc.jpg", "https://www.zhihu.com/"},
		{"https://i0.hdslb.com/bfs/archive/abc.jpg", "https://www.bilibili.com/"},
		{"https://img1.doubanio.com/view/photo/s_ratio_poster/public/p123.jpg", "https://www.douban.com/"},
		{"https://example.com/pic.jpg", ""},
	}

	for _, tt := range tests {
		actual := p.GetRefererForURL(tt.url)
		if actual != tt.expected {
			t.Errorf("GetRefererForURL(%q) = %q, want %q", tt.url, actual, tt.expected)
		}
	}
}

func TestGetCandidateURLs(t *testing.T) {
	p := NewImageProxy()

	// Short-lived token URL from XHS
	raw := "http://sns-webpic-qc.xhscdn.com/202610081146/acdc8493c5f7e534028a454e231c6737/notes_uhdr/1040g3qo31o6q9skq882049e8t481u6sq519hbeg"
	candidates := p.GetCandidateURLs(raw)

	expected0 := "https://sns-img-qc.xhscdn.com/notes_uhdr/1040g3qo31o6q9skq882049e8t481u6sq519hbeg"
	expected1 := "https://ci.xiaohongshu.com/notes_uhdr/1040g3qo31o6q9skq882049e8t481u6sq519hbeg"

	if len(candidates) < 3 {
		t.Fatalf("Expected at least 3 candidates, got %d", len(candidates))
	}
	if candidates[0] != expected0 {
		t.Errorf("Candidate[0] = %s, want %s", candidates[0], expected0)
	}
	if candidates[1] != expected1 {
		t.Errorf("Candidate[1] = %s, want %s", candidates[1], expected1)
	}
}

func TestServeHTTP_MockUpstream(t *testing.T) {
	// Create a mock upstream server
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") != "https://weibo.com/" {
			t.Errorf("Expected Referer: https://weibo.com/, got %s", r.Header.Get("Referer"))
		}
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fake-image-bytes"))
	}))
	defer mockUpstream.Close()

	p := NewImageProxy()
	p.client = mockUpstream.Client() // Bypass SSRF check in local test
	p.rules = []RefererRule{
		{
			Pattern: nil, // catch-all or default
			Referer: "https://weibo.com/",
		},
	}
	// Give a valid pattern matching 127.0.0.1
	var err error
	p.rules[0].Pattern, err = regexp.Compile(`127\.0\.0\.1`)
	if err != nil {
		t.Fatal(err)
	}

	// Perform request to image proxy
	req := httptest.NewRequest(http.MethodGet, "/api/proxy/image?url="+mockUpstream.URL+"/test.png", nil)
	rr := httptest.NewRecorder()

	p.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d, body: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Cache-Control") != "public, max-age=2592000, immutable" {
		t.Errorf("Expected Cache-Control header, got %s", rr.Header().Get("Cache-Control"))
	}
	if rr.Body.String() != "fake-image-bytes" {
		t.Errorf("Expected body fake-image-bytes, got %s", rr.Body.String())
	}
}
