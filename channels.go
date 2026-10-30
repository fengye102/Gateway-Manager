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
	LoginHint       string            `json:"login_hint,omitempty"`
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
			LoginURL:  "https://opencode.ai/",
			LoginHint: "匿名渠道，一般无需登录；token 填 public 即可",
			Auth:      ChannelAuth{Type: "bearer", Header: "Authorization", Prefix: "Bearer "},
			ExtraHeaders: map[string]string{
				"User-Agent":         "opencode/latest/2.0.29/cli",
				"x-opencode-client":  "cli",
				"x-opencode-project": "gateway-manager",
				"Accept":             "application/json, text/event-stream",
				"x-session-affinity": "gateway-manager",
			},
			Models: []string{
				"big-pickle", "deepseek-v4.1-flash-free", "glm-5.3-flash-free", "mimo-v2.6-flash-free",
				"flint-40b-free", "nemotron-3.5-super-vl-free", "deepseek-v4.1-flash", "deepseek-v4-pro",
				"glm-5.3", "kimi-k2.6", "minimax-m3", "qwen3.8-max",
			},
			Protocol: "openai",
			Notes:    "匿名 token 填 public；免费档被上游锁定为「仅官方客户端可用」，第三方调用返回 429 配额限制，付费模型需填有效 key",
		},
		{
			ID: "zhipu-autoclaw", Name: "智谱 AutoGLM / autoclaw", Vendor: "zhipu",
			BaseURL: "https://autoglm-acceleration-api.zhipuai.cn", ChatPath: "/autoclaw-proxy/proxy/autoclaw/chat/completions",
			LoginURL:  "https://agentmore.chatglm.cn/",
			LoginHint: "智谱账号登录（AutoGLM 网页版）",
			Auth:      ChannelAuth{Type: "header", Header: "X-Authorization", Prefix: "Bearer "},
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
			ID: "xunfei-loomy", Name: "科大讯飞 Loomy", Vendor: "xunfei",
			BaseURL: "https://loomyad.xunfei.cn", ChatPath: "/api/v1/chat/completions",
			LoginURL:  "https://loomyad.xunfei.cn/",
			LoginHint: "讯飞账号登录",
			Auth:      ChannelAuth{Type: "query", QueryKey: "token"},
			Protocol:  "openai",
			Notes:     "OpenAI 兼容；token 走查询参数",
		},
		{
			ID: "deepseek-web", Name: "DeepSeek 网页版", Vendor: "deepseek",
			BaseURL: "https://chat.deepseek.com", ChatPath: "/api/v0/chat/completion",
			LoginURL:        "https://chat.deepseek.com/sign_in",
			LoginHint:       "手机号 / 邮箱 / 微信登录；登录成功后页面会跳到对话页",
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
			ID: "runable", Name: "Runable", Vendor: "runable",
			BaseURL: "https://api.runable.com", ChatPath: "/api/chat/text",
			LoginURL:     "https://runable.com/?refer_code=KL5ASK",
			LoginHint:    "Google 邮箱或 Magic Link 登录",
			Auth:         ChannelAuth{Type: "cookie", Header: "Cookie"},
			ExtraHeaders: map[string]string{"x-runable-device": "dweb"},
			Models: []string{
				"runable/runner-chat", "anthropic/claude-haiku-5.5", "anthropic/claude-sonnet-5.5",
				"anthropic/claude-opus-5.5", "anthropic/claude-fable-5.1", "zai/glm-5.3-flash",
				"zai/glm-5.3", "openai/gpt-6.1-sol", "openai/gpt-6-astra",
			},
			Protocol: "openai",
			Notes:    "已适配：cookie + /api/chat/text + AI SDK 流；modelId 原样透传，清单见 /api/rpc/public/models/getAIChatModels",
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
