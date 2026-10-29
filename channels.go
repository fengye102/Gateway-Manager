package main

import (
	"context"
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
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Vendor          string            `json:"vendor"`
	BaseURL         string            `json:"base_url"`
	ChatPath        string            `json:"chat_path"`
	LoginURL        string            `json:"login_url,omitempty"`
	TokenStorageKey string            `json:"token_storage_key,omitempty"`
	ModelsPath      string            `json:"models_path,omitempty"`
	Auth            ChannelAuth       `json:"auth"`
	ExtraHeaders    map[string]string `json:"extra_headers,omitempty"`
	Models          []string          `json:"models,omitempty"`
	Protocol        string            `json:"protocol"` // openai | needs_adapter
	Notes           string            `json:"notes,omitempty"`
}

// Channel 用户配置的渠道实例（带凭证），会被当成上游使用
type Channel struct {
	ID       string            `json:"id"`
	Preset   string            `json:"preset"`
	Name     string            `json:"name,omitempty"`
	Token    string            `json:"token,omitempty"`
	Cookie   string            `json:"cookie,omitempty"`
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
			LoginURL: "https://opencode.ai/",
			Auth:     ChannelAuth{Type: "bearer", Header: "Authorization", Prefix: "Bearer "},
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
			LoginURL: "https://agentmore.chatglm.cn/",
			Auth:     ChannelAuth{Type: "header", Header: "X-Authorization", Prefix: "Bearer "},
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
			LoginURL: "https://www.workbuddy.ai/",
			Auth:     ChannelAuth{Type: "cookie", Header: "Cookie"},
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
			LoginURL: "https://xiaohuanxiong.com/",
			Auth:     ChannelAuth{Type: "header", Header: "X-Auth-Token", Prefix: ""},
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
			LoginURL: "https://loomyad.xunfei.cn/",
			Auth:     ChannelAuth{Type: "query", QueryKey: "token"},
			Protocol: "openai",
			Notes:    "OpenAI 兼容；token 走查询参数",
		},
		{
			ID: "deepseek-web", Name: "DeepSeek 网页版", Vendor: "deepseek",
			BaseURL: "https://chat.deepseek.com", ChatPath: "/api/v0/chat/completion",
			LoginURL:        "https://chat.deepseek.com/sign_in",
			ModelsPath:      "/api/v0/users/current",
			TokenStorageKey: "localStorage.userToken",
			Auth:            ChannelAuth{Type: "bearer", Header: "Authorization", Prefix: "Bearer "},
			ExtraHeaders: map[string]string{
				"x-client-platform": "web",
				"x-client-version":  "2.5.0",
				"x-client-locale":   "zh_CN",
			},
			Models:   []string{"deepseek-chat", "deepseek-reasoner"},
			Protocol: "openai",
			Notes:    "已适配：DeepSeekHashV1 PoW（23 轮 Keccak）+ <｜User｜> prompt + patch 流转 OpenAI",
		},
		{
			ID: "tabbit", Name: "Tabbit", Vendor: "tabbit",
			BaseURL: "https://web.tabbit.com", ChatPath: "/api/v3/chat/runs",
			LoginURL:     "https://web.tabbit.com/",
			Auth:         ChannelAuth{Type: "cookie", Header: "Cookie"},
			ExtraHeaders: map[string]string{"x-tabbit-client-platform": "android"},
			Protocol:     "needs_adapter",
			Notes:        "需要 X-Req-Ctx / X-Nonce / X-Timestamp / X-Signature 签名头 + 私有响应格式，尚未适配",
		},
		{
			ID: "catpaw", Name: "美团 CatPaw", Vendor: "catpaw",
			BaseURL: "https://ai.catpaw.meituan.com", ChatPath: "/api/chat",
			LoginURL: "https://ai.catpaw.meituan.com/",
			Auth:     ChannelAuth{Type: "cookie", Header: "Cookie"},
			Protocol: "needs_adapter",
			Notes:    "私有协议（SSE 自定义字段），尚未适配",
		},
		{
			ID: "dangbei", Name: "当贝 AI", Vendor: "dangbei",
			BaseURL: "https://ai-api.dangbei.net", ChatPath: "/ai-search",
			LoginURL: "https://ai.dangbei.com/chat",
			Auth:     ChannelAuth{Type: "header", Header: "Authorization", Prefix: "Bearer "},
			Protocol: "needs_adapter",
			Notes:    "私有协议，尚未适配",
		},
		{
			ID: "runable", Name: "Runable", Vendor: "runable",
			BaseURL: "https://api.runable.com", ChatPath: "/api/chat/text",
			LoginURL:     "https://runable.com/",
			Auth:         ChannelAuth{Type: "bearer", Header: "Authorization", Prefix: "Bearer "},
			ExtraHeaders: map[string]string{"x-runable-device": "dweb"},
			Protocol:     "needs_adapter",
			Notes:        "私有协议，尚未适配",
		},
		{
			ID: "meoo", Name: "阿里 秒悟 Meoo", Vendor: "meoo",
			BaseURL: "https://meoo.com", ChatPath: "/api/v1/agent/models",
			LoginURL: "https://meoo.com/",
			Auth:     ChannelAuth{Type: "cookie", Header: "Cookie"},
			Protocol: "needs_adapter",
			Notes:    "私有协议（会话 + 轮询），尚未适配",
		},
		{
			ID: "wenxin", Name: "百度 文心", Vendor: "baidu",
			BaseURL: "https://chat.baidu.com", ChatPath: "/aichat/api/conversation",
			LoginURL: "https://wappass.baidu.com/passport/?login&u=https%3A%2F%2Fwenxin.baidu.com%2F",
			Auth:     ChannelAuth{Type: "cookie", Header: "Cookie"},
			Protocol: "needs_adapter",
			Notes:    "私有协议，尚未适配",
		},
	}
}

// channelAdapters：需要协议转换的渠道实现
var channelAdapters = map[string]func(*Server, context.Context, Channel, ChannelPreset, []byte) (*http.Response, error){
	"deepseek-web": (*Server).deepseekForward,
	"runable":      (*Server).runableForward,
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
