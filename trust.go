package web

import (
	"net"
	"strings"
)

// TrustedProxies resolves the client IP behind reverse proxies (参照 ghttp
// 的 trust 设计)：仅当直连对端位于受信 CIDR 内时，才接受 X-Forwarded-For /
// X-Real-IP；否则一律使用直连地址，防止伪造。解析结果经 Req.ClientIP()
// 读取。CIDR 语法错误在注册期 panic（程序错误）。
func TrustedProxies(cidrs ...string) Middleware {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic("web: TrustedProxies: " + err.Error())
		}
		nets = append(nets, n)
	}
	return func(next Handler) Handler {
		return func(c *Ctx) error {
			ip := hostOnly(c.Req.RemoteAddr)
			trusted := false
			if parsed := net.ParseIP(ip); parsed != nil {
				for _, n := range nets {
					if n.Contains(parsed) {
						trusted = true
						break
					}
				}
			}
			switch {
			case trusted && c.Req.Header.Get("X-Forwarded-For") != "":
				c.clientIP = rightmostUntrusted(c.Req.Header.Get("X-Forwarded-For"), nets)
			case trusted && c.Req.Header.Get("X-Real-IP") != "":
				c.clientIP = c.Req.Header.Get("X-Real-IP")
			default:
				c.clientIP = ip
			}
			return next(c)
		}
	}
}

// hostOnly strips the port from host:port.
func hostOnly(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// rightmostUntrusted walks X-Forwarded-For from the right (closest to the
// server) and returns the first entry that is not itself a trusted proxy.
func rightmostUntrusted(xff string, nets []*net.IPNet) string {
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip := strings.TrimSpace(parts[i])
		parsed := net.ParseIP(ip)
		if parsed == nil {
			return ip
		}
		trusted := false
		for _, n := range nets {
			if n.Contains(parsed) {
				trusted = true
				break
			}
		}
		if !trusted {
			return ip
		}
	}
	return ""
}
