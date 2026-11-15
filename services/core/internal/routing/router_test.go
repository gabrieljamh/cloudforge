package routing

import (
	"cloudforge/core/internal/cloudflare"
	"cloudforge/core/internal/model"
	"cloudforge/core/internal/persistence"
	"cloudforge/core/internal/supervisor"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"testing"
)

func TestOfflineRouteIsPreserved(t *testing.T) {
	dir := t.TempDir()
	s, e := persistence.Open(filepath.Join(dir, "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p := model.Project{ID: "project", Name: "Offline", Path: dir, Runtime: "python", DesiredState: "stopped"}
	if e = s.SaveProject(p); e != nil {
		t.Fatal(e)
	}
	route := model.Route{ID: "route", ProjectID: p.ID, Hostname: "app.example.com", Port: 3000}
	if e = s.SaveRoute(route); e != nil {
		t.Fatal(e)
	}
	sup := supervisor.New(s, dir)
	sup.Add(p)
	defer sup.Shutdown()
	cf := cloudflare.New(s, dir, "http://127.0.0.1:28932", "test")
	router := &Router{Store: s, Cloud: cf, Supervisor: sup, Dashboard: http.NotFoundHandler()}
	req := httptest.NewRequest("GET", "https://app.example.com/", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 503 {
		t.Fatalf("offline application should return 503, got %d", w.Code)
	}
	saved, _ := s.Routes()
	if len(saved) != 1 {
		t.Fatal("offline route was removed")
	}
	req = httptest.NewRequest("GET", "https://unconfigured.example.com/", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatal("unknown host was routed")
	}
}

func TestForwardHeadersCarryCloudflareVisitor(t *testing.T) {
	got := make(chan http.Header, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Clone()
	}))
	defer backend.Close()
	target, _ := url.Parse(backend.URL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	original := proxy.Director
	proxy.Director = func(q *http.Request) {
		original(q)
		forwardHeaders(q)
	}
	send := func(headers map[string]string) http.Header {
		req := httptest.NewRequest("GET", "https://app.example.com/", nil)
		req.RemoteAddr = "127.0.0.1:50000"
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		proxy.ServeHTTP(httptest.NewRecorder(), req)
		return <-got
	}
	spoof := map[string]string{"X-Forwarded-For": "6.6.6.6", "X-Real-Ip": "6.6.6.6", "Forwarded": "for=6.6.6.6", "X-Forwarded-Host": "evil.example"}
	cases := []struct {
		name, connecting, forwarded, real string
	}{
		{"ipv4", " 203.0.113.7 ", "203.0.113.7, 127.0.0.1", "203.0.113.7"},
		{"ipv6", "2001:db8::1", "2001:db8::1, 127.0.0.1", "2001:db8::1"},
		{"missing", "", "127.0.0.1", ""},
		{"invalid", "203.0.113.7, 6.6.6.6", "127.0.0.1", ""},
	}
	for _, c := range cases {
		headers := map[string]string{}
		for k, v := range spoof {
			headers[k] = v
		}
		if c.connecting != "" {
			headers["Cf-Connecting-Ip"] = c.connecting
		}
		h := send(headers)
		if h.Get("X-Forwarded-For") != c.forwarded || h.Get("X-Real-Ip") != c.real {
			t.Fatalf("%s: X-Forwarded-For=%q X-Real-Ip=%q", c.name, h.Get("X-Forwarded-For"), h.Get("X-Real-Ip"))
		}
		if c.real == "" && h.Get("Cf-Connecting-Ip") != "" {
			t.Fatalf("%s: invalid CF-Connecting-IP forwarded", c.name)
		}
		if h.Get("Forwarded") != "" || h.Get("X-Forwarded-Host") != "" || h.Get("X-Forwarded-Proto") != "https" {
			t.Fatalf("%s: forwarding headers not normalized: %v", c.name, h)
		}
	}
}
