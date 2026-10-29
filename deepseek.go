package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const deepseekDefaultBase = "https://chat.deepseek.com"

var deepseekBaseOverride = ""

func deepseekBaseURL() string {
	if deepseekBaseOverride != "" {
		return deepseekBaseOverride
	}
	return deepseekDefaultBase
}

const deepseekBase = deepseekDefaultBase

type deepseekClient struct {
	Token  string
	Cookie string
	HC     *http.Client
}

func newDeepseekClient(token, cookie string, timeout time.Duration) *deepseekClient {
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	return &deepseekClient{Token: token, Cookie: cookie, HC: &http.Client{Timeout: timeout}}
}

func (c *deepseekClient) headers(extra map[string]string) http.Header {
	h := http.Header{}
	h.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	h.Set("Accept", "application/json")
	h.Set("Accept-Language", "zh-CN,zh;q=0.9")
	h.Set("Content-Type", "application/json")
	h.Set("x-client-platform", "web")
	h.Set("x-client-version", "2.5.0")
	h.Set("x-client-locale", "zh_CN")
	h.Set("Origin", deepseekBase)
	h.Set("Referer", deepseekBaseURL()+"/")
	if c.Token != "" {
		h.Set("authorization", "Bearer "+c.Token)
	}
	if c.Cookie != "" {
		h.Set("Cookie", c.Cookie)
	}
	for k, v := range extra {
		h.Set(k, v)
	}
	return h
}

func (c *deepseekClient) postJSON(ctx context.Context, path string, body any, extra map[string]string) ([]byte, int, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deepseekBaseURL()+path, bytes.NewReader(raw))
	if err != nil {
		return nil, 0, err
	}
	req.Header = c.headers(extra)
	resp, err := c.HC.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%s: %w", req.URL.String(), err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return out, resp.StatusCode, nil
}

func (c *deepseekClient) createSession(ctx context.Context) (string, error) {
	out, status, err := c.postJSON(ctx, "/api/v0/chat_session/create", map[string]any{"agent": "chat"}, nil)
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("创建会话失败 HTTP %d: %s", status, truncateForReport(string(out), 200))
	}
	var payload struct {
		Data struct {
			BizData struct {
				Session struct {
					ID string `json:"id"`
				} `json:"chat_session"`
				ChatSessionID string `json:"chat_session_id"`
			} `json:"biz_data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return "", fmt.Errorf("解析会话响应失败: %v", err)
	}
	id := payload.Data.BizData.Session.ID
	if id == "" {
		id = payload.Data.BizData.ChatSessionID
	}
	if id == "" {
		return "", fmt.Errorf("会话 ID 为空：%s", truncateForReport(string(out), 200))
	}
	return id, nil
}

type deepseekChallenge struct {
	Algorithm  string `json:"algorithm"`
	Challenge  string `json:"challenge"`
	Salt       string `json:"salt"`
	ExpireAt   int64  `json:"expire_at"`
	Difficulty int64  `json:"difficulty"`
	Signature  string `json:"signature"`
	TargetPath string `json:"target_path"`
}

func solveDeepSeekPow(chal deepseekChallenge, limit int64) (int64, bool) {
	return solveDeepSeekPowV1(chal.Challenge, chal.Salt, chal.ExpireAt, limit)
}

// solveDebug 矩阵扫描：轮数 / padding / 字节序，用于对齐真实服务端规则
func solveDebug(chal deepseekChallenge, limit int64) map[string]int64 {
	res := map[string]int64{}
	target, err := hex.DecodeString(chal.Challenge)
	if err != nil || len(target) != 32 {
		return res
	}
	if limit <= 0 {
		limit = 144000
	}
	prefix := chal.Salt + "_" + strconv.FormatInt(chal.ExpireAt, 10) + "_"
	var want [32]byte
	copy(want[:], target)
	variants := []struct {
		name    string
		pad     byte
		from    int
		swapped bool
	}{
		{"r23_pad06", 0x06, 1, false},
		{"r24_pad06", 0x06, 0, false},
		{"r24_pad01", 0x01, 0, false},
		{"r23_pad01", 0x01, 1, false},
		{"r23_pad06_sw", 0x06, 1, true},
		{"r24_pad06_sw", 0x06, 0, true},
	}
	for _, v := range variants {
		res[v.name] = -1
	}
	buf := make([]byte, 0, len(prefix)+12)
	buf = append(buf, prefix...)
	baseLen := len(buf)
	for n := int64(0); n < limit; n++ {
		buf = append(buf[:baseLen], strconv.FormatInt(n, 10)...)
		var hit [32]byte
		copy(hit[:], target)
		for _, v := range variants {
			if res[v.name] >= 0 {
				continue
			}
			sum := keccakGeneric(buf, v.pad, v.from, 24)
			if v.swapped {
				sum = swap8Bytes(sum)
			}
			if sum == hit {
				res[v.name] = n
			}
		}
		done := true
		for _, v := range variants {
			if res[v.name] < 0 {
				done = false
				break
			}
		}
		if done {
			break
		}
	}
	return res
}

func swap8Bytes(b [32]byte) [32]byte {
	out := b
	for i := 0; i+8 <= len(out); i += 8 {
		for j := 0; j < 4; j++ {
			out[i+j], out[i+7-j] = out[i+7-j], out[i+j]
		}
	}
	return out
}

func (c *deepseekClient) powHeader(ctx context.Context) (string, error) {
	out, status, err := c.postJSON(ctx, "/api/v0/chat/create_pow_challenge", map[string]any{"target_path": "/api/v0/chat/completion"}, nil)
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("获取 PoW 挑战失败 HTTP %d: %s", status, truncateForReport(string(out), 200))
	}
	var payload struct {
		Data struct {
			BizData struct {
				Challenge deepseekChallenge `json:"challenge"`
			} `json:"biz_data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return "", fmt.Errorf("解析 PoW 挑战失败: %v", err)
	}
	chal := payload.Data.BizData.Challenge
	if chal.Algorithm != "" && chal.Algorithm != "DeepSeekHashV1" {
		return "", fmt.Errorf("未知 PoW 算法：%s", chal.Algorithm)
	}
	limit := chal.Difficulty
	if limit <= 0 {
		limit = 144000
	}
	answer, ok := solveDeepSeekPow(chal, limit)
	if !ok {
		return "", fmt.Errorf("PoW 求解失败（难度 %d）", limit)
	}
	if chal.TargetPath == "" {
		chal.TargetPath = "/api/v0/chat/completion"
	}
	payloadObj := map[string]any{
		"algorithm":   chal.Algorithm,
		"challenge":   chal.Challenge,
		"salt":        chal.Salt,
		"answer":      answer,
		"signature":   chal.Signature,
		"target_path": chal.TargetPath,
	}
	raw, _ := json.Marshal(payloadObj)
	return base64.StdEncoding.EncodeToString(raw), nil
}

// buildDeepSeekPrompt：按原版 APK 的 <｜User｜> / <｜Assistant｜> 格式拼 prompt
func buildDeepSeekPrompt(messages []map[string]any) string {
	var b strings.Builder
	system := ""
	for _, m := range messages {
		role, _ := m["role"].(string)
		content := messageText(m["content"])
		switch role {
		case "system":
			if system == "" {
				system = content
			} else {
				system += "\n\n" + content
			}
		case "assistant":
			b.WriteString("<｜Assistant｜>")
			b.WriteString(content)
			b.WriteString("<｜end▁of▁sentence｜>")
		default:
			b.WriteString("<｜User｜>")
			b.WriteString(content)
		}
	}
	if system != "" {
		b.WriteString("<｜System｜>" + system)
	}
	b.WriteString("<｜Assistant｜>")
	return b.String()
}

func messageText(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case []any:
		var sb strings.Builder
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					sb.WriteString(s)
				}
			}
		}
		return sb.String()
	case map[string]any:
		if s, ok := v["text"].(string); ok {
			return s
		}
	}
	return ""
}

func (c *deepseekClient) complete(ctx context.Context, sessionID, prompt string, thinking, search bool) (*http.Response, error) {
	pow, err := c.powHeader(ctx)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{
		"chat_session_id":   sessionID,
		"parent_message_id": nil,
		"prompt":            prompt,
		"ref_file_ids":      []string{},
		"thinking_enabled":  thinking,
		"search_enabled":    search,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deepseekBaseURL()+"/api/v0/chat/completion", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = c.headers(map[string]string{
		"x-ds-pow-response": pow,
		"Accept":            "text/event-stream",
	})
	resp, err := c.HC.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", req.URL.String(), err)
	}
	return resp, nil
}
