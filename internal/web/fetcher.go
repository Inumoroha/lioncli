package web

import (
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"golang.org/x/text/encoding/htmlindex"
)

const (
	// maxFetchBytes 响应体上限 5MB，超出部分被截断。
	maxFetchBytes = 5 << 20
	// fetchTimeout 整体超时 30 秒（含连接、读取）。
	fetchTimeout = 30 * time.Second
)

// RawResponse 是一次抓取的原始结果。Body 已按解析出的字符集转成 UTF-8。
type RawResponse struct {
	URL         string
	StatusCode  int
	ContentType string
	Charset     string // 实际采用的字符集；兜底时为 "utf-8"
	Body        string
	Truncated   bool // body 是否因超过 5MB 被截断
}

// Fetcher 抽象「给一个 URL，发 GET 拿回原始响应」。
type Fetcher interface {
	Fetch(url string) (*RawResponse, error)
}

// HTTPFetcher 用标准库 net/http 实现 Fetcher（Go 里 OkHttp 的等价物）。
type HTTPFetcher struct {
	client *http.Client
	policy *NetworkPolicy
}

func NewHTTPFetcher() *HTTPFetcher {
	policy := NewNetworkPolicy()

	dialer := &net.Dialer{Timeout: fetchTimeout}
	// 拨号阶段（DNS 解析之后、真正建连时）对目标 IP 再查一次，
	// 拦掉「公网域名重定向 / 解析到内网」这类绕过 CheckURL 的 SSRF。
	dialer.Control = func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		if ip := net.ParseIP(host); ip != nil && isBlockedIP(ip) {
			return fmt.Errorf("禁止连接内网/本机地址: %s", host)
		}
		return nil
	}

	return &HTTPFetcher{
		policy: policy,
		client: &http.Client{
			Timeout: fetchTimeout,
			Transport: &http.Transport{
				DialContext:         dialer.DialContext,
				TLSHandshakeTimeout: fetchTimeout,
			},
		},
	}
}

// Fetch 发 GET 请求并拿回原始 HTML 字符串。
func (f *HTTPFetcher) Fetch(url string) (*RawResponse, error) {
	// 关卡一：URL 安全检查（协议白名单 + 内网屏蔽，防 SSRF）。
	if err := f.policy.CheckURL(url); err != nil {
		return nil, err
	}
	// 关卡二：请求频率限制。
	if !f.policy.Allow() {
		return nil, ErrRateLimited
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	// 给个常见 UA，避免部分站点直接拒绝默认的 Go-http-client。
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; teacli-web-fetch/1.0)")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 多读 1 字节用于判断是否超限：读到 maxFetchBytes+1 说明实际更长，需截断。
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取响应体失败: %w", err)
	}
	truncated := false
	if len(raw) > maxFetchBytes {
		raw = raw[:maxFetchBytes]
		truncated = true
	}

	contentType := resp.Header.Get("Content-Type")
	charsetName, body := decodeBody(raw, contentType)

	return &RawResponse{
		URL:         url,
		StatusCode:  resp.StatusCode,
		ContentType: contentType,
		Charset:     charsetName,
		Body:        body,
		Truncated:   truncated,
	}, nil
}

// decodeBody 按「Content-Type 的 charset 参数优先、全失败兜底 UTF-8」解析字符集，
// 并把响应体转成 UTF-8 字符串，返回实际采用的字符集名。
func decodeBody(raw []byte, contentType string) (charset, body string) {
	cs := ""
	if contentType != "" {
		if _, params, err := mime.ParseMediaType(contentType); err == nil {
			cs = strings.ToLower(strings.TrimSpace(params["charset"]))
		}
	}

	// 没声明、或本就是 UTF-8：直接按 UTF-8 处理，不转码。
	if cs == "" || cs == "utf-8" || cs == "utf8" {
		return "utf-8", string(raw)
	}

	// 声明了其它字符集：能识别就转码；查不到或转码失败一律兜底 UTF-8。
	enc, err := htmlindex.Get(cs)
	if err != nil {
		return "utf-8", string(raw)
	}
	decoded, err := enc.NewDecoder().Bytes(raw)
	if err != nil {
		return "utf-8", string(raw)
	}
	return cs, string(decoded)
}
