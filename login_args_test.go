package main

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestBuildLoginBrowserArgsRootHeadless(t *testing.T) {
	// 与 launchLoginBrowser 的调用约定一致：headless 时以 about:blank 启动
	args := buildLoginBrowserArgs("/tmp/p", "about:blank", true, true, "")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--no-sandbox") {
		t.Fatalf("root 下必须带 --no-sandbox: %v", args)
	}
	if !strings.Contains(joined, "--headless=new") || !strings.Contains(joined, "--window-size=1180,860") {
		t.Fatalf("headless 模式参数缺失: %v", args)
	}
	if !strings.Contains(joined, "about:blank") {
		t.Fatalf("headless 模式应以 about:blank 启动: %v", args)
	}
}

func TestBuildLoginBrowserArgsDesktop(t *testing.T) {
	args := buildLoginBrowserArgs("/tmp/p", "https://example.com", false, false, "")
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--no-sandbox") || strings.Contains(joined, "--headless") {
		t.Fatalf("桌面模式不应带 headless/sandbox 参数: %v", args)
	}
	if !strings.Contains(joined, "https://example.com") {
		t.Fatalf("桌面模式应直接打开登录页: %v", args)
	}
}

func TestBuildLoginBrowserArgsProxy(t *testing.T) {
	// 站点按出口 IP 风控时，受控浏览器必须走指定出口
	args := buildLoginBrowserArgs("/tmp/p", "https://example.com", false, false, "socks5://127.0.0.1:1080")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--proxy-server=socks5://127.0.0.1:1080") {
		t.Fatalf("配置了代理就必须带 --proxy-server: %v", args)
	}
	plain := strings.Join(buildLoginBrowserArgs("/tmp/p", "https://example.com", false, false, ""), " ")
	if strings.Contains(plain, "--proxy-server") {
		t.Fatalf("未配置代理时不应带 --proxy-server: %s", plain)
	}
}

func TestProxyForBrowser(t *testing.T) {
	cases := []struct {
		raw   string
		want  string
		isErr bool
	}{
		{raw: "", want: ""},
		{raw: "socks5://127.0.0.1:1080", want: "socks5://127.0.0.1:1080"},
		{raw: "socks5h://127.0.0.1:1080", want: "socks5://127.0.0.1:1080"},
		{raw: "http://127.0.0.1:3128", want: "http://127.0.0.1:3128"},
		{raw: "ftp://127.0.0.1:21", isErr: true},
		{raw: "socks5://u:p@127.0.0.1:1080", isErr: true},
		{raw: "not a url", isErr: true},
	}
	for _, c := range cases {
		got, err := proxyForBrowser(c.raw)
		if c.isErr {
			if err == nil {
				t.Fatalf("%q 应当报错，实际返回 %q", c.raw, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q 不应报错: %v", c.raw, err)
		}
		if got != c.want {
			t.Fatalf("%q 期望 %q，实际 %q", c.raw, c.want, got)
		}
	}
}

func TestHTTPClientWithProxy(t *testing.T) {
	if tr := httpClientWithProxy(time.Second, "").Transport; tr != nil {
		t.Fatalf("空代理应当直连（Transport 为 nil）")
	}
	client := httpClientWithProxy(time.Second, "socks5://127.0.0.1:1080")
	tr, ok := client.Transport.(*http.Transport)
	if !ok || tr.Proxy == nil {
		t.Fatalf("配置了代理时 Transport 必须带 Proxy: %#v", client.Transport)
	}
	u, err := tr.Proxy(nil)
	if err != nil || u == nil || u.Host != "127.0.0.1:1080" {
		t.Fatalf("代理地址解析错误: %v %v", u, err)
	}
}

func TestAccountErrorIs(t *testing.T) {
	err := &accountError{reason: "额度不足", cooldown: 30 * time.Minute}
	if !errors.Is(err, errAccountUnavailable) {
		t.Fatalf("accountError 必须能被 errors.Is 识别为 errAccountUnavailable")
	}
	if got := accountCooldown(err); got != 30*time.Minute {
		t.Fatalf("冷却时长应当透传: %v", got)
	}
	if got := accountCooldown(errAccountUnavailable); got != 0 {
		t.Fatalf("普通错误不应带建议冷却: %v", got)
	}
}

func TestKeyPoolCoolAndAvailable(t *testing.T) {
	pool := newKeyPool([]string{"acc-a", "acc-b"}, "score", 30*time.Second)
	if pool.availableCount() != 2 {
		t.Fatalf("初始两个账号都应可用: %d", pool.availableCount())
	}
	st, ok := pool.pick()
	if !ok || st == nil {
		t.Fatal("应当取到账号")
	}
	pool.cool(st, 10*time.Minute, "积分已用完")
	if pool.availableCount() != 1 {
		t.Fatalf("冷却一个账号后应剩 1 个可用: %d", pool.availableCount())
	}
	// 冷却中的账号不能再被选中
	for i := 0; i < 5; i++ {
		picked, ok := pool.pick()
		if !ok {
			t.Fatalf("仍有可用账号，不应返回 false")
		}
		if picked.key == st.key {
			t.Fatalf("冷却中的账号被再次选中: %s", picked.key)
		}
	}
	// 全部冷却后应拒绝出号
	for _, it := range pool.items {
		pool.cool(it, 10*time.Minute, "测试")
	}
	if _, ok := pool.pick(); ok {
		t.Fatal("所有账号冷却中时不应再出号")
	}
}

func TestKeyPoolSpreadsAcrossAccounts(t *testing.T) {
	// 新加的账号必须立即参与轮转，而不是被满分的健康账号永远压住
	pool := newKeyPool([]string{"acc-a", "acc-b"}, "score", 30*time.Second)
	used := map[string]int{}
	for i := 0; i < 6; i++ {
		st, ok := pool.pick()
		if !ok {
			t.Fatalf("第 %d 次取号失败", i)
		}
		used[st.key]++
		pool.report(st, 200, 10*time.Millisecond, "")
	}
	if used["acc-a"] == 0 || used["acc-b"] == 0 {
		t.Fatalf("两个账号都应被用到，实际分布 %v", used)
	}
	if used["acc-a"] != 3 || used["acc-b"] != 3 {
		t.Fatalf("6 次请求应均分到两个账号，实际 %v", used)
	}
}
