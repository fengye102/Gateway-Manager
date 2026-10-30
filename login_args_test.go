package main

import (
	"strings"
	"testing"
)

func TestBuildLoginBrowserArgsRootHeadless(t *testing.T) {
	// 与 launchLoginBrowser 的调用约定一致：headless 时以 about:blank 启动
	args := buildLoginBrowserArgs("/tmp/p", "about:blank", true, true)
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
	args := buildLoginBrowserArgs("/tmp/p", "https://example.com", false, false)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--no-sandbox") || strings.Contains(joined, "--headless") {
		t.Fatalf("桌面模式不应带 headless/sandbox 参数: %v", args)
	}
	if !strings.Contains(joined, "https://example.com") {
		t.Fatalf("桌面模式应直接打开登录页: %v", args)
	}
}
