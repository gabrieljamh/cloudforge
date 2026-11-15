package routing

import (
	"cloudforge/core/internal/cloudflare"
	"cloudforge/core/internal/persistence"
	"cloudforge/core/internal/supervisor"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// responseHeaderTimeout bounds how long an application may take to start answering.
// AI proxies wait for the model's first output before sending headers, which can take
// minutes (queues, reasoning), so this is generous. A client that disconnects still cancels
// the request at once, and Cloudflare applies its own shorter limit to tunnelled traffic.
var responseHeaderTimeout = 15 * time.Minute

// newTransport connects to applications on this machine: an application that is not
// listening fails within 5 seconds, a slow one may take up to responseHeaderTimeout.
func newTransport() *http.Transport {
	return &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext, ResponseHeaderTimeout: responseHeaderTimeout, MaxIdleConns: 64, MaxIdleConnsPerHost: 8, IdleConnTimeout: 60 * time.Second}
}

type Router struct {
	Store       persistence.Repository
	Cloud       *cloudflare.Manager
	Supervisor  *supervisor.Supervisor
	Dashboard   http.Handler
	Development bool
	once        sync.Once
	transport   *http.Transport
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	host := strings.ToLower(req.Host)
	if h, _, e := net.SplitHostPort(host); e == nil {
		host = h
	}
	cfg := r.Cloud.Config()
	if host == cfg.AdminHost || (r.Development && (host == "127.0.0.1" || host == "localhost")) {
		r.Dashboard.ServeHTTP(w, req)
		return
	}
	routes, e := r.Store.Routes()
	if e != nil {
		r.showError(w, req, "service")
		return
	}
	for _, route := range routes {
		if route.Hostname != host {
			continue
		}
		if !r.Supervisor.Online(route.ProjectID) {
			w.Header().Set("Retry-After", "30")
			r.showError(w, req, "offline")
			return
		}
		target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", route.Port))
		proxy := httputil.NewSingleHostReverseProxy(target)
		r.once.Do(func() { r.transport = newTransport() })
		proxy.Transport = r.transport
		original := proxy.Director
		proxy.Director = func(q *http.Request) {
			original(q)
			forwardHeaders(q)
		}
		proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
			r.showError(w, req, "port")
		}
		proxy.ServeHTTP(w, req)
		return
	}
	r.showError(w, req, "not-found")
}

// forwardHeaders replaces client-supplied forwarding headers with the visitor's address as
// Cloudflare reported it. The public listener only accepts connections from this machine,
// where cloudflared is the client, so CF-Connecting-IP is set by Cloudflare's edge and can be
// trusted here. X-Forwarded-For starts with the visitor and the reverse proxy then appends the
// tunnel's own address (127.0.0.1), giving the usual "visitor, proxy" chain. Without a valid
// CF-Connecting-IP the visitor is unknown and only the tunnel's address is forwarded.
func forwardHeaders(q *http.Request) {
	q.Header.Del("Forwarded")
	q.Header.Del("X-Forwarded-Host")
	q.Header.Del("X-Forwarded-For")
	q.Header.Del("X-Real-Ip")
	q.Header.Set("X-Forwarded-Proto", "https")
	visitor := net.ParseIP(strings.TrimSpace(q.Header.Get("Cf-Connecting-Ip")))
	if visitor == nil {
		q.Header.Del("Cf-Connecting-Ip")
		return
	}
	q.Header.Set("Cf-Connecting-Ip", visitor.String())
	q.Header.Set("X-Forwarded-For", visitor.String())
	q.Header.Set("X-Real-Ip", visitor.String())
}
