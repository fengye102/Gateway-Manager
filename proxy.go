package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// errAccountUnavailable：渠道账号本身不可用（额度耗尽 / 被站点风控封禁）。
// 需要立刻冷却该账号并换同渠道的下一个账号，而不是把整个上游判定为故障。
var errAccountUnavailable = errors.New("账号不可用")

// accountError 账号级错误的带建议冷却时长实现。
// errors.Is(err, errAccountUnavailable) 恒为 true，同时能把「积分用完」
// 和「短时限流」区分成不同的冷却时间。
type accountError struct {
	reason   string
	cooldown time.Duration
}

func (e *accountError) Error() string { return e.reason }

func (e *accountError) Is(target error) bool { return target == errAccountUnavailable }

// accountCooldown 取账号级错误建议的冷却时长（没有建议时返回 0，由调用方兜底）
func accountCooldown(err error) time.Duration {
	var ae *accountError
	if errors.As(err, &ae) {
		return ae.cooldown
	}
	return 0
}

// httpClientWithProxy 构造带出口代理的 http.Client。
// proxy 支持 http:// / https:// / socks5:// / socks5h://；留空则直连。
// 部分站点按出口 IP 风控（如 Genspark 直接 403 数据中心 IP），
// 这类渠道的「登录浏览器」和「API 转发」必须走同一个出口：
// cf_clearance 之类 cookie 绑定 IP + UA，换出口等于换身份。
func httpClientWithProxy(timeout time.Duration, proxy string) *http.Client {
	client := &http.Client{Timeout: timeout}
	u, err := parseProxyURL(proxy)
	if err != nil || u == nil {
		return client
	}
	client.Transport = &http.Transport{
		Proxy:                 http.ProxyURL(u),
		MaxIdleConns:          32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   20 * time.Second,
		ExpectContinueTimeout: 5 * time.Second,
	}
	return client
}

// parseProxyURL 解析并校验代理地址（空字符串 → nil, nil 即直连）
func parseProxyURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("代理地址无效（应形如 socks5://127.0.0.1:1080）：%s", raw)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
		return u, nil
	default:
		return nil, fmt.Errorf("不支持的代理协议 %s（支持 http/https/socks5/socks5h）", u.Scheme)
	}
}

// normalizeProxy 规范化用户填写的代理地址，空值表示直连
func normalizeProxy(raw string) (string, error) {
	u, err := parseProxyURL(raw)
	if err != nil {
		return "", err
	}
	if u == nil {
		return "", nil
	}
	return u.String(), nil
}

// proxyForBrowser 把代理地址转成 Chromium --proxy-server 接受的形态。
// Chromium 只认 scheme://host:port（socks5h 视同 socks5），
// 且不支持在 URL 里带账号密码，需要额外的认证扩展——这里直接拒绝，避免静默失效。
func proxyForBrowser(raw string) (string, error) {
	u, err := parseProxyURL(raw)
	if err != nil {
		return "", err
	}
	if u == nil {
		return "", nil
	}
	if u.User != nil {
		return "", fmt.Errorf("受控浏览器暂不支持带账号密码的代理，请用本机免认证的 socks5 出口")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "socks5h" {
		scheme = "socks5"
	}
	return scheme + "://" + u.Host, nil
}
