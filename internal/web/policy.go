package web

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// rateWindow / rateMaxHits：60 秒内最多 30 次请求。
	rateWindow  = 60 * time.Second
	rateMaxHits = 30
)

// ErrRateLimited 在窗口内请求数超限时返回。
var ErrRateLimited = errors.New("请求过于频繁：60 秒内最多 30 次请求")

// NetworkPolicy 在 HTTP 请求发出前做两道关卡：
//  1. URL 安全检查：只放行 http/https，屏蔽 localhost、127.0.0.1、内网网段，防 SSRF；
//  2. 请求频率限制：滑动窗口，默认 60s 内最多 30 次，防止 Agent 陷入重试循环狂刷同一站点被封 IP。
type NetworkPolicy struct {
	window  time.Duration
	maxHits int

	mu   sync.Mutex
	hits []time.Time
}

func NewNetworkPolicy() *NetworkPolicy {
	return &NetworkPolicy{window: rateWindow, maxHits: rateMaxHits}
}

// CheckURL 是第一道关卡：只放行 http/https，并拒绝指向本机/内网的地址。
// 注意：对主机名而言，最终连到的 IP 还会在拨号阶段（dialer.Control）再查一次，
// 以防「公网域名重定向 / DNS 解析到内网」这类绕过本检查的 SSRF。
func (p *NetworkPolicy) CheckURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("URL 解析失败: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		// 拦掉 file://、ftp:// 等。
		return fmt.Errorf("不允许的协议 %q：只支持 http/https", u.Scheme)
	}

	host := strings.ToLower(u.Hostname())
	if host == "" {
		return errors.New("URL 缺少主机名")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("禁止访问本机地址: %s", host)
	}
	// 字面量 IP 直接判定；主机名留给拨号阶段复查。
	if ip := net.ParseIP(host); ip != nil && isBlockedIP(ip) {
		return fmt.Errorf("禁止访问内网/本机地址: %s", host)
	}
	return nil
}

// Allow 是第二道关卡：滑动窗口限流。窗口内已达上限返回 false。
func (p *NetworkPolicy) Allow() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-p.window)

	// 丢弃窗口外的旧记录。hits 按时间递增，找到第一个仍在窗口内的位置即可。
	drop := 0
	for drop < len(p.hits) && !p.hits[drop].After(cutoff) {
		drop++
	}
	p.hits = p.hits[drop:]

	if len(p.hits) >= p.maxHits {
		return false
	}
	p.hits = append(p.hits, now)
	return true
}

// isBlockedIP 判断 IP 是否落在禁止访问的范围：环回、私有网段、链路本地
// （含 169.254.169.254 这类云元数据地址）、未指定地址。
// 覆盖 127.0.0.1、10.x、172.16-31.x、192.168.x、::1、fc00::/7、fe80::/10 等。
func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified()
}
