package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const runableDevice = "dweb/a69d4f72ddd711c660237adaf5c76287ac5473f5"

const runableDefaultBase = "https://api.runable.com"

var runableBaseOverride = ""

func runableBaseURL() string {
	if runableBaseOverride != "" {
		return runableBaseOverride
	}
	return runableDefaultBase
}

type runableClient struct {
	Cookie string
	HC     *http.Client
}

func newRunableClient(cookie string, timeout time.Duration, proxy string) *runableClient {
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	return &runableClient{Cookie: cookie, HC: httpClientWithProxy(timeout, proxy)}
}

func (c *runableClient) headers() http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "text/event-stream")
	h.Set("User-Agent", "Mozilla/5.0 (Linux; Android 14) RunableGateway/1.0")
	h.Set("Origin", "https://runable.com")
	h.Set("Referer", "https://runable.com/")
	h.Set("x-runable-device", runableDevice)
	if c.Cookie != "" {
		h.Set("Cookie", c.Cookie)
	}
	return h
}

func (c *runableClient) session(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, runableBaseURL()+"/api/auth/get-session", nil)
	if err != nil {
		return err
	}
	req.Header = c.headers()
	req.Header.Set("Accept", "application/json")
	resp, err := c.HC.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("Runable 会话校验失败 HTTP %d：%s", resp.StatusCode, truncateForReport(string(raw), 200))
	}
	if !strings.Contains(string(raw), "session") || strings.Contains(string(raw), "session:null") {
		return fmt.Errorf("Runable 会话为空，请重新网页登录")
	}
	return nil
}

func (c *runableClient) chat(ctx context.Context, messageID, model, prompt string) (*http.Response, error) {
	body, err := json.Marshal(map[string]any{
		"id":      messageID,
		"trigger": "submit-message",
		"modelId": model,
		"message": map[string]any{
			"id":   randomUUID(),
			"role": "user",
			"parts": []any{
				map[string]any{"type": "text", "text": prompt},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, runableBaseURL()+"/api/chat/text", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = c.headers()
	resp, err := c.HC.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", req.URL.String(), err)
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		switch {
		case resp.StatusCode == 401 || strings.Contains(string(raw), "SESSION_NOT_FOUND"):
			return nil, &accountError{reason: "Runable 登录态已失效，请重新网页登录", cooldown: 30 * time.Minute}
		case resp.StatusCode == 402 || strings.Contains(string(raw), "OUT_OF_CREDITS"):
			return nil, &accountError{reason: "Runable 积分已耗尽", cooldown: 30 * time.Minute}
		case resp.StatusCode == 429:
			return nil, &accountError{reason: "Runable 限流，请稍后重试", cooldown: time.Minute}
		}
		return nil, fmt.Errorf("Runable 返回 HTTP %d：%s", resp.StatusCode, truncateForReport(string(raw), 200))
	}
	return resp, nil
}

func randomUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func buildRunablePrompt(messages []map[string]any) string {
	parts := make([]string, 0, len(messages))
	for _, m := range messages {
		text := messageText(m["content"])
		if strings.TrimSpace(text) == "" {
			continue
		}
		role, _ := m["role"].(string)
		switch role {
		case "assistant":
			parts = append(parts, "[assistant]: "+text)
		case "tool":
			parts = append(parts, "[tool]: "+text)
		default:
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

type runableUsage struct {
	Input  int
	Output int
}

func (u runableUsage) Total() int { return u.Input + u.Output }

func parseRunableStream(r io.Reader, onDelta func(string)) runableUsage {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var usage runableUsage
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(data), &obj); err != nil {
			if txt := looseText(data); txt != "" {
				onDelta(txt)
			}
			continue
		}
		if typ, _ := obj["type"].(string); typ == "custom" {
			if in := digFloat(obj, "providerMetadata", "anthropic", "usage", "input_tokens"); in > 0 {
				usage.Input = int(in)
			}
			if out := digFloat(obj, "providerMetadata", "anthropic", "usage", "output_tokens"); out > 0 {
				usage.Output = int(out)
			}
		}
		if u := digFloat(obj, "usage", "totalTokens"); u > 0 {
			usage.Output = int(u)
		}
		if u := digFloat(obj, "usage", "outputTokens"); u > 0 {
			usage.Output = int(u)
		}
		if txt := runableDelta(obj); txt != "" {
			onDelta(txt)
		}
	}
	return usage
}

func runableDelta(obj map[string]any) string {
	typ, _ := obj["type"].(string)
	switch typ {
	case "text-delta", "text":
		for _, key := range []string{"delta", "textDelta", "text", "content"} {
			if s, ok := obj[key].(string); ok && s != "" {
				return s
			}
		}
		return ""
	case "", "start", "finish", "step-start", "step-finish", "reasoning-delta", "usage":
	}
	for _, key := range []string{"delta", "textDelta"} {
		if s, ok := obj[key].(string); ok && s != "" {
			return s
		}
	}
	if t, ok := obj["text"].(string); ok && typ == "" {
		return t
	}
	return ""
}

func digFloat(obj map[string]any, path ...string) float64 {
	var cur any = obj
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return 0
		}
		cur = m[key]
	}
	if f, ok := cur.(float64); ok {
		return f
	}
	return 0
}

func looseText(data string) string {
	if idx := strings.Index(data, ":"); idx > 0 {
		head := strings.TrimSpace(data[:idx])
		if _, err := fmt.Sscanf(head, "%d", new(int)); err == nil {
			rest := strings.TrimSpace(data[idx+1:])
			rest = strings.Trim(rest, "\x22")
			rest = strings.ReplaceAll(rest, "\\\x22", "\x22")
			return rest
		}
	}
	if strings.HasPrefix(data, "\x22") && strings.HasSuffix(data, "\x22") {
		return strings.Trim(data, "\x22")
	}
	return ""
}

func (s *Server) runableForward(ctx context.Context, ch Channel, preset ChannelPreset, payload []byte, credential string) (*http.Response, error) {
	var req map[string]any
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, fmt.Errorf("请求体不是 JSON：%v", err)
	}
	model, _ := req["model"].(string)
	if model == "" {
		model = "runable/runner-chat"
	}
	stream := jsonBoolField(req, "stream")
	messages := extractMessages(req)
	if len(messages) == 0 {
		return nil, fmt.Errorf("messages 为空")
	}
	prompt := buildRunablePrompt(messages)
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("请求里没有可发送的内容")
	}

	cookie := strings.TrimSpace(credential)
	if cookie == "" {
		cookie = strings.TrimSpace(ch.Cookie)
	}
	if cookie == "" {
		cookie = strings.TrimSpace(ch.Token)
	}
	client := newRunableClient(cookie, 300*time.Second, ch.Proxy)
	messageID := randomUUID()
	resp, err := client.chat(ctx, messageID, model, prompt)
	if err != nil {
		return nil, err
	}

	id := "chatcmpl-rnbl-" + fmt.Sprint(time.Now().UnixMilli())
	created := time.Now().Unix()
	if !stream {
		defer resp.Body.Close()
		var sb strings.Builder
		usage := parseRunableStream(resp.Body, func(d string) { sb.WriteString(d) })
		return jsonResponse(map[string]any{
			"id": id, "object": "chat.completion", "created": created, "model": model,
			"choices": []any{map[string]any{
				"index": 0, "message": map[string]any{"role": "assistant", "content": sb.String()}, "finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": usage.Input, "completion_tokens": usage.Output, "total_tokens": usage.Total()},
		}), nil
	}

	pr, pw := io.Pipe()
	header := http.Header{}
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-cache")
	go func() {
		defer resp.Body.Close()
		defer pw.Close()
		bw := bufio.NewWriter(pw)
		writeChunk := func(delta map[string]any, finish any) {
			raw, _ := json.Marshal(map[string]any{
				"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
				"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
			})
			_, _ = bw.WriteString("data: ")
			_, _ = bw.Write(raw)
			_, _ = bw.WriteString("\n\n")
		}
		writeChunk(map[string]any{"role": "assistant", "content": ""}, nil)
		usage := parseRunableStream(resp.Body, func(d string) {
			writeChunk(map[string]any{"content": d}, nil)
			_ = bw.Flush()
		})
		writeChunk(map[string]any{}, "stop")
		if usage.Total() > 0 {
			raw, _ := json.Marshal(map[string]any{
				"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
				"choices": []any{},
				"usage":   map[string]any{"prompt_tokens": usage.Input, "completion_tokens": usage.Output, "total_tokens": usage.Total()},
			})
			_, _ = bw.WriteString("data: ")
			_, _ = bw.Write(raw)
			_, _ = bw.WriteString("\n\n")
		}
		_, _ = bw.WriteString("data: [DONE]\n\n")
		_ = bw.Flush()
	}()
	return &http.Response{StatusCode: 200, Header: header, Body: pr}, nil
}
