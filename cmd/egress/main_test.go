package main

import (
	"net"
	"testing"
)

func TestIsPrivate(t *testing.T) {
	cases := []struct {
		ip      string
		private bool
	}{
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"172.32.0.1", false}, // 出 172.16/12 范围
		{"192.168.1.1", true},
		{"169.254.169.254", true}, // 云元数据，必须拒
		{"127.0.0.1", true},
		{"100.64.0.1", true},   // CGNAT
		{"100.128.0.1", false}, // 出 CGNAT 范围
		{"8.8.8.8", false},     // 公网
		{"1.1.1.1", false},
		{"::1", true},
		{"fc00::1", true},       // IPv6 ULA
		{"fe80::1", true},       // link-local
		{"2606:4700::1", false}, // 公网 IPv6
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if ip == nil {
			t.Fatalf("无法解析 %s", c.ip)
		}
		if got := isPrivate(ip); got != c.private {
			t.Errorf("isPrivate(%s)=%v, 期望 %v", c.ip, got, c.private)
		}
	}
}

func TestPermitAllowlist(t *testing.T) {
	p := &proxy{allow: map[string]bool{"host.docker.internal:8182": true}}
	// 白名单的控制平面应放行（即便它解析到私网）
	if !p.permit("host.docker.internal:8182") {
		t.Error("控制平面白名单应放行")
	}
	// 未白名单的私网地址应拒绝
	if p.permit("10.0.0.5:80") {
		t.Error("私网地址应拒绝")
	}
	if p.permit("169.254.169.254:80") {
		t.Error("云元数据应拒绝")
	}
}
