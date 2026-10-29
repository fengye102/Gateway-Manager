package main

import (
	"net/http"
	"net/url"
	"strings"
)

// ChannelAuth 描述凭证怎么放进请求
type ChannelAuth struct {
	Type     string `json:"type"` // bearer | header | cookie | query | none
	Header   string `json:"header,omitempty"`
	Prefix   string `json:"prefix,omitempty"`
	QueryKey string `json:"query_key,omitempty"`
}

// ChannelPreset 是内置渠道模板（端口自原版 APK 的实际请求路径 / 请求头）
type ChannelPreset struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Vendor       string            `json:"vendor"`
	BaseURL      string            `json:"base_url"`
	ChatPath     string            `json:"chat_path"`
	ModelsPath   string            `json:"models_path,omitempty"`
	Auth         ChannelAuth       `json:"auth"`
	ExtraHeaders map[string]string `json:"extra_headers,omitempty"`
	Models       []string          `json:"models,omitempty"`
	Protocol     string            `json:"protocol"` // openai | needs_adapter
	Notes        string            `json:"notes,omitempty"`
}

// Channel 用户配置的渠道实例（带凭证），会被当成上游使用
type Channel struct {
	ID       string            `json:"id"`
	Preset   string            `json:"preset"`
	Name     string            `json:"name,omitempty"`
	Token    string            `json:"token,omitempty"`
	BaseURL  string            `json:"base_url,omitempty"`
	Models   []string          `json:"models,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Priority int               `json:"priority"`
	Weight   int               `json:"weight"`
	Enabled  bool              `json:"enabled"`
}

// 内置渠道：地址 / 路径 / 认证方式 / 额外请求头来自原版 APK 的反编译结果
func builtinChannelPresets() []ChannelPreset {
	return []ChannelPreset{
		{
			ID: "opencode-zen", Name: "OpenCode Zen（匿名免费档）", Vendor: "opencode",
			BaseURL: "https://opencode.ai", ChatPath: "/inference/v1/chat/completions", ModelsPath: "/zen/v1/models",
			Auth: ChannelAuth{Type: "bearer", Header: "Authorization", Prefix: "Bearer "},
			ExtraHeaders: map[string]string{
				"User-Agent":         "opencode/latest/2.0.29/cli",
				"x-opencode-client":  "cli",
				"x-opencode-project": "gateway-manager",
				"Accept":             "application/json, text/event-stream",
				"x-session-affinity": "gateway-manager",
			},
			Models:   []string{"grok-code", "qwen3-coder", "kimi-k2", "glm-4.6"},
			Protocol: "openai",
			Notes:    "匿名渠道：token 填 public 即可；官方 OpenAI 兼容，可直接用",
		},
		{
			ID: "zhipu-autoclaw", Name: "智谱 AutoGLM / autoclaw", Vendor: "zhipu",
			BaseURL: "https://autoglm-acceleration-api.zhipuai.cn", ChatPath: "/autoclaw-proxy/proxy/autoclaw/chat/completions",
			Auth: ChannelAuth{Type: "header", Header: "X-Authorization", Prefix: "Bearer "},
			ExtraHeaders: map[string]string{
				"X-Product":     "autoclaw",
				"X-Client-Type": "pc",
				"X-Tm":          "win",
				"X-Version":     "1.17.8",
				"X-Lang":        "zh-CN",
				"X-Channel":     "official",
				"x_trace_id":    "autoclaw-desktop",
			},
			Protocol: "openai",
			Notes:    "OpenAI 兼容；凭证从桌面端 autoclaw 登录态获取（X-Authorization Bearer）",
		},
		{
			ID: "workbuddy", Name: "WorkBuddy / CodeBuddy", Vendor: "workbuddy",
			BaseURL: "https://www.workbuddy.ai", ChatPath: "/v2/chat/completions",
			Auth: ChannelAuth{Type: "cookie", Header: "Cookie"},
			ExtraHeaders: map[string]string{
				"X-Product":        "SaaS",
				"X-Requested-With": "XMLHttpRequest",
				"Accept":           "application/json, text/plain, */*",
			},
			Protocol: "openai",
			Notes:    "OpenAI 兼容；需要 cookie 以及 X-User-Id / X-Enterprise-Id / X-Tenant-Id（在渠道 headers 里补）",
		},
		{
			ID: "raccoon", Name: "商汤 小浣熊", Vendor: "raccoon",
			BaseURL: "https://xiaohuanxiong.com", ChatPath: "/api/web/llm/v2/chat/completions",
			Auth: ChannelAuth{Type: "header", Header: "X-Auth-Token", Prefix: ""},
			ExtraHeaders: map[string]string{
				"X-Client-Platform": "desktop-windows",
				"X-Client-Version":  "v1.0.38",
			},
			Protocol: "openai",
			Notes:    "OpenAI 兼容；还需 X-Org-Code（在渠道 headers 里补）",
		},
		{
			ID: "xunfei-loomy", Name: "科大讯飞 Loomy", Vendor: "xunfei",
			BaseURL: "https://loomyad.xunfei.cn", ChatPath: "/api/v1/chat/completions",
			Auth:     ChannelAuth{Type: "query", QueryKey: "token"},
			Protocol: "openai",
			Notes:    "OpenAI 兼容；token 走查询参数",
		},
		{
			ID: "deepseek-web", Name: "DeepSeek 网页版", Vendor: "deepseek",
			BaseURL: "https://chat.deepseek.com", ChatPath: "/api/v0/chat/completion",
			ModelsPath: "/api/v0/users/current",
			Auth:       ChannelAuth{Type: "bearer", Header: "Authorization", Prefix: "Bearer "},
			ExtraHeaders: map[string]string{
				"x-client-platform": "web",
				"x-client-version":  "2.5.0",
				"x-client-locale":   "zh_CN",
			},
			Models:   []string{"deepseek-chat", "deepseek-reasoner"},
			Protocol: "needs_adapter",
			Notes:    "需要 PoW 挑战头 x-ds-pow-response + 专用 prompt 格式（<｜User｜> 标记），尚未适配",
		},
		{
			ID: "tabbit", Name: "Tabbit", Vendor: "tabbit",
			BaseURL: "https://web.tabbit.com", ChatPath: "/api/v3/chat/runs",
			Auth:         ChannelAuth{Type: "cookie", Header: "Cookie"},
			ExtraHeaders: map[string]string{"x-tabbit-client-platform": "android"},
			Protocol:     "needs_adapter",
			Notes:        "需要 X-Req-Ctx / X-Nonce / X-Timestamp / X-Signature 签名头 + 私有响应格式，尚未适配",
		},
		{
			ID: "trae", Name: "Trae", Vendor: "trae",
			BaseURL: "https://api.trae.cn", ChatPath: "/cloudide/api/v3/trae/chat",
			Auth: ChannelAuth{Type: "header", Header: "X-Cloudide-Token", Prefix: ""},
			ExtraHeaders: map[string]string{
				"X-App-Id":      "6eefa01c-1036-4c7e-9ca5-d891f63bfcd8",
				"X-App-Version": "default",
				"X-Device-Type": "windows",
				"X-Ide-Version": "stable",
			},
			Protocol: "needs_adapter",
			Notes:    "需要 X-Uid / X-Machine-Id / x-device-id 等设备头 + IDE 专用协议，尚未适配",
		},
		{
			ID: "catpaw", Name: "美团 CatPaw", Vendor: "catpaw",
			BaseURL: "https://ai.catpaw.meituan.com", ChatPath: "/api/chat",
			Auth:     ChannelAuth{Type: "cookie", Header: "Cookie"},
			Protocol: "needs_adapter",
			Notes:    "私有协议（SSE 自定义字段），尚未适配",
		},
		{
			ID: "dangbei", Name: "当贝 AI", Vendor: "dangbei",
			BaseURL: "https://ai-api.dangbei.net", ChatPath: "/ai-search",
			Auth:     ChannelAuth{Type: "header", Header: "Authorization", Prefix: "Bearer "},
			Protocol: "needs_adapter",
			Notes:    "私有协议，尚未适配",
		},
		{
			ID: "runable", Name: "Runable", Vendor: "runable",
			BaseURL: "https://api.runable.com", ChatPath: "/api/chat/text",
			Auth:         ChannelAuth{Type: "bearer", Header: "Authorization", Prefix: "Bearer "},
			ExtraHeaders: map[string]string{"x-runable-device": "dweb"},
			Protocol:     "needs_adapter",
			Notes:        "私有协议，尚未适配",
		},
		{
			ID: "meoo", Name: "阿里 秒悟 Meoo", Vendor: "meoo",
			BaseURL: "https://meoo.com", ChatPath: "/api/v1/agent/models",
			Auth:     ChannelAuth{Type: "cookie", Header: "Cookie"},
			Protocol: "needs_adapter",
			Notes:    "私有协议（会话 + 轮询），尚未适配",
		},
		{
			ID: "wenxin", Name: "百度 文心", Vendor: "baidu",
			BaseURL: "https://chat.baidu.com", ChatPath: "/aichat/api/conversation",
			Auth:     ChannelAuth{Type: "cookie", Header: "Cookie"},
			Protocol: "needs_adapter",
			Notes:    "私有协议，尚未适配",
		},
	}
}

func findPreset(id string) (ChannelPreset, bool) {
	for _, p := range builtinChannelPresets() {
		if p.ID == id {
			return p, true
		}
	}
	return ChannelPreset{}, false
}

// 把用户配置的渠道折算成一条上游（参与选路、冷却、预算、指标）
func (c Channel) toUpstream(preset ChannelPreset) Upstream {
	name := c.Name
	if name == "" {
		name = preset.Name
	}
	base := c.BaseURL
	if base == "" {
		base = preset.BaseURL
	}
	models := c.Models
	if len(models) == 0 {
		models = preset.Models
	}
	return Upstream{
		ID:          "ch-" + c.ID,
		Name:        "渠道:" + name,
		BaseURL:     base,
		APIKeys:     keyList(c.Token),
		Models:      models,
		Priority:    c.Priority,
		Weight:      c.Weight,
		Enabled:     c.Enabled,
		Headers:     mergeHeaders(preset.ExtraHeaders, c.Headers),
		ChannelID:   c.ID,
		ChannelPath: preset.ChatPath,
	}
}

func keyList(token string) []string {
	if strings.TrimSpace(token) == "" {
		return []string{}
	}
	return []string{token}
}

func mergeHeaders(base map[string]string, override map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		if strings.TrimSpace(v) == "" {
			delete(out, k)
			continue
		}
		out[k] = v
	}
	return out
}

// applyChannelAuth 把凭证塞进 header / cookie / query
func applyChannelAuth(req *http.Request, preset ChannelPreset, token string) {
	if token == "" {
		return
	}
	switch preset.Auth.Type {
	case "bearer":
		header := preset.Auth.Header
		if header == "" {
			header = "Authorization"
		}
		prefix := preset.Auth.Prefix
		if prefix == "" {
			prefix = "Bearer "
		}
		req.Header.Set(header, prefix+token)
	case "header":
		header := preset.Auth.Header
		if header == "" {
			header = "Authorization"
		}
		req.Header.Set(header, preset.Auth.Prefix+token)
	case "cookie":
		header := preset.Auth.Header
		if header == "" {
			header = "Cookie"
		}
		req.Header.Set(header, token)
	case "query":
		key := preset.Auth.QueryKey
		if key == "" {
			key = "token"
		}
		q := req.URL.Query()
		q.Set(key, token)
		req.URL.RawQuery = q.Encode()
	}
}

// channelChatURL 拼出渠道的聊天端点（保留 base_url 里可能带的路径）
func channelChatURL(base string, chatPath string) string {
	return joinURL(strings.TrimRight(base, "/"), chatPath)
}

var _ = url.Parse
