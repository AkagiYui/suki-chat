// Command egress 是会话容器的"只放行互联网"出网代理（deny-by-default）。
//
// 会话容器（runner/浏览器）位于 internal 网络（无任何外联），唯一出口就是本代理。
// 策略：放行 控制平面白名单 + 公网；拒绝 一切私网（RFC1918 / 169.254 元数据 / 回环 /
// IPv6 ULA）。这样被 prompt 注入的 agent 无法访问宿主内网、云元数据或横向打其他服务。
package main

import (
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	listen := env("EGRESS_LISTEN", ":8888")
	// 允许直连的白名单 host[:port]（如控制平面 host.docker.internal:8182），逗号分隔。
	allow := map[string]bool{}
	for _, h := range strings.Split(os.Getenv("EGRESS_ALLOW_HOSTS"), ",") {
		if h = strings.TrimSpace(h); h != "" {
			allow[h] = true
		}
	}

	p := &proxy{allow: allow}
	srv := &http.Server{Addr: listen, Handler: p, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("egress proxy 监听 %s；白名单: %v", listen, keys(allow))
	log.Fatal(srv.ListenAndServe())
}

type proxy struct{ allow map[string]bool }

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if r.Method == http.MethodConnect {
		host = r.URL.Host // CONNECT 形如 example.com:443
	}
	if !p.permit(host) {
		log.Printf("DENY %s %s", r.Method, host)
		http.Error(w, "egress denied (private destination blocked)", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodConnect {
		p.handleConnect(w, host)
		return
	}
	p.handleHTTP(w, r)
}

// permit 判定目标是否放行：白名单直接通过；否则解析 IP，私网拒绝、公网放行。
func (p *proxy) permit(hostport string) bool {
	host, port := splitHostPort(hostport)
	if p.allow[hostport] || p.allow[host] || (port != "" && p.allow[host+":"+port]) {
		return true
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if isPrivate(ip) { // 任一解析结果落入私网即拒绝（防 DNS rebinding）
			return false
		}
	}
	return true
}

func (p *proxy) handleConnect(w http.ResponseWriter, host string) {
	dst, err := net.DialTimeout("tcp", host, 15*time.Second)
	if err != nil {
		http.Error(w, "dial failed", http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "no hijack", http.StatusInternalServerError)
		dst.Close()
		return
	}
	src, _, err := hj.Hijack()
	if err != nil {
		dst.Close()
		return
	}
	src.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	go pipe(dst, src)
	go pipe(src, dst)
}

func (p *proxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	outReq, err := http.NewRequest(r.Method, r.URL.String(), r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	outReq.Header = r.Header.Clone()
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(outReq)
	if err != nil {
		http.Error(w, "upstream failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// isPrivate 判定 IP 是否属于不可外达的私有/特殊范围。
func isPrivate(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true // 含 169.254.0.0/16 云元数据、127.0.0.0/8、::1
	}
	if ip4 := ip.To4(); ip4 != nil {
		switch {
		case ip4[0] == 10:
			return true // 10.0.0.0/8
		case ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31:
			return true // 172.16.0.0/12
		case ip4[0] == 192 && ip4[1] == 168:
			return true // 192.168.0.0/16
		case ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127:
			return true // 100.64.0.0/10 CGNAT
		}
		return false
	}
	// IPv6 ULA fc00::/7
	return len(ip) == net.IPv6len && (ip[0]&0xfe) == 0xfc
}

func pipe(dst, src net.Conn) {
	defer dst.Close()
	defer src.Close()
	io.Copy(dst, src)
}

func splitHostPort(hp string) (host, port string) {
	if h, p, err := net.SplitHostPort(hp); err == nil {
		return h, p
	}
	return hp, ""
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
