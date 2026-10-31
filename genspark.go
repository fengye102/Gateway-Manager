package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	gensparkDefaultModel = "gpt-5"
	gensparkAskPath      = "/api/agent/ask_proxy"
	gensparkOrigin       = "https://www.genspark.ai"
	gensparkReferer      = "https://www.genspark.ai/agents?type=ai_chat"
	gensparkUserAgent    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"
	gensparkCreditMark   = "积分已用完"
)

var gensparkModelAlias = map[string]string{
	"claude-haiku-4-5": "claude-4-5-haiku",
	"claude-opus-4-5":  "claude-opus-4-6",
}

type gensparkClient struct {
	Cookie string
	HC     *http.Client
}

func newGensparkClient(cookie string, timeout time.Duration, proxy string) *gensparkClient {
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	return &gensparkClient{Cookie: cookie, HC: httpClientWithProxy(timeout, proxy)}
}

func gensparkTrace() (string, string) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		stamp := fmt.Sprintf("%032x", time.Now().UnixNano())
		return "|" + stamp + ".0000000000000000", stamp
	}
	hexed := hex.EncodeToString(buf)
	return "|" + hexed + "." + hexed[:16], hexed
}

func (c *gensparkClient) headers() http.Header {
	rid, trace := gensparkTrace()
	h := http.Header{}
	h.Set("User-Agent", gensparkUserAgent)
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "text/event-stream")
	h.Set("Origin", gensparkOrigin)
	h.Set("Referer", gensparkReferer)
	h.Set("request-id", rid)
	h.Set("traceparent", "00-"+trace+"-"+trace[:16]+"-01")
	if c.Cookie != "" {
		h.Set("Cookie", c.Cookie)
	}
	return h
}

func gensparkResolveModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return gensparkDefaultModel
	}
	if alias, ok := gensparkModelAlias[model]; ok {
		return alias
	}
	return model
}

func buildGensparkBody(model string, messages []map[string]any) []byte {
	clean := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		role, _ := m["role"].(string)
		if role == "" {
			continue
		}
		text := messageText(m["content"])
		if strings.TrimSpace(text) == "" {
			continue
		}
		clean = append(clean, map[string]any{"role": role, "content": text})
	}
	raw, _ := json.Marshal(map[string]any{
		"ai_chat_model":                   model,
		"ai_chat_enable_search":           false,
		"ai_chat_disable_personalization": false,
		"use_moa_proxy":                   false,
		"moa_models":                      []any{},
		"writingContent":                  nil,
		"sas_ask_origin":                  "typed",
		"type":                            "ai_chat",
		"is_private":                      true,
		"messages":                        clean,
	})
	return raw
}

func (c *gensparkClient) ask(ctx context.Context, base string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+gensparkAskPath, bytes.NewReader(body))
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
		switch resp.StatusCode {
		case 401:
			return nil, &accountError{reason: "Genspark 登录态已失效（HTTP 401），请重新网页登录", cooldown: 30 * time.Minute}
		case 403:
			// 可能是账号被风控，也可能是出口代理本身被拦：冷却短一些，先换号再试
			return nil, &accountError{reason: "Genspark 返回 HTTP 403（账号被风控或出口 IP 被拦）", cooldown: 10 * time.Minute}
		case 429:
			return nil, &accountError{reason: "Genspark 限流，请稍后重试", cooldown: time.Minute}
		}
		// Cloudflare 在 cookie 失效时常回 400 "bad request cf"：同样属于账号级问题
		if resp.StatusCode == 400 && strings.Contains(strings.ToLower(string(raw)), "cf") {
			return nil, &accountError{reason: "Genspark 拒绝了该账号的会话（HTTP 400 cf），请重新网页登录", cooldown: 30 * time.Minute}
		}
		return nil, fmt.Errorf("Genspark 返回 HTTP %d：%s", resp.StatusCode, truncateForReport(string(raw), 200))
	}
	return resp, nil
}

type gensparkResult struct {
	Content  string
	Deltas   string
	Throttle string
	Failure  string
}

func parseGensparkStream(r io.Reader, onDelta func(string)) gensparkResult {
	out := gensparkResult{}
	var sb strings.Builder
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var evt map[string]any
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &evt) != nil {
			continue
		}
		switch evt["type"] {
		case "message_field":
			name, _ := evt["field_name"].(string)
			if name != "content" {
				continue
			}
			if v, ok := evt["field_value"].(string); ok && out.Content == "" {
				out.Content = v
			}
		case "message_field_delta":
			name, _ := evt["field_name"].(string)
			if name != "content" {
				continue
			}
			delta, _ := evt["delta"].(string)
			if delta == "" {
				continue
			}
			sb.WriteString(delta)
			if onDelta != nil {
				onDelta(delta)
			}
		case "message_result":
			msg, ok := evt["message"].(map[string]any)
			if !ok {
				continue
			}
			content, _ := msg["content"].(string)
			switch {
			case strings.Contains(content, gensparkCreditMark):
				out.Throttle = truncateForReport(content, 160)
			case strings.Contains(content, "too quickly"), strings.Contains(content, "Rate limit"):
				out.Throttle = truncateForReport(content, 160)
			case out.Content == "" && sb.Len() == 0 && content != "":
				out.Content = content
			}
		case "error":
			raw, _ := json.Marshal(evt)
			out.Failure = truncateForReport(string(raw), 200)
		}
	}
	out.Deltas = sb.String()
	return out
}

func (s *Server) gensparkForward(ctx context.Context, ch Channel, preset ChannelPreset, payload []byte, credential string) (*http.Response, error) {
	var req map[string]any
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, fmt.Errorf("请求体不是 JSON：%v", err)
	}
	model, _ := req["model"].(string)
	model = gensparkResolveModel(model)
	stream := jsonBoolField(req, "stream")
	messages := extractMessages(req)
	if len(messages) == 0 {
		return nil, fmt.Errorf("messages 为空")
	}

	cookie := strings.TrimSpace(credential)
	if cookie == "" {
		cookie = strings.TrimSpace(ch.Cookie)
	}
	if cookie == "" {
		cookie = strings.TrimSpace(ch.Token)
	}
	if cookie == "" {
		return nil, fmt.Errorf("Genspark 渠道缺少 cookie，请先网页登录")
	}

	base := strings.TrimSuffix(strings.TrimSpace(preset.BaseURL), "/")
	if base == "" {
		base = gensparkOrigin
	}
	body := buildGensparkBody(model, messages)
	promptTokens := estimatePromptTokens(gensparkPromptText(messages))

	client := newGensparkClient(cookie, 300*time.Second, ch.Proxy)
	resp, err := client.ask(ctx, base, body)
	if err != nil {
		return nil, err
	}

	id := "chatcmpl-gs-" + fmt.Sprint(time.Now().UnixMilli())
	created := time.Now().Unix()

	if !stream {
		defer resp.Body.Close()
		var sb strings.Builder
		result := parseGensparkStream(resp.Body, func(d string) { sb.WriteString(d) })
		if result.Throttle != "" {
			// 积分耗尽属于账号级不可用（长冷却）；「问得太快」只是短时限流。
			if strings.Contains(result.Throttle, gensparkCreditMark) {
				return nil, &accountError{reason: "Genspark 额度不足：" + result.Throttle, cooldown: 30 * time.Minute}
			}
			return nil, &accountError{reason: "Genspark 限流：" + result.Throttle, cooldown: time.Minute}
		}
		if result.Failure != "" && sb.Len() == 0 && result.Content == "" {
			return nil, fmt.Errorf("Genspark 上游报错：%s", result.Failure)
		}
		text := sb.String()
		if text == "" {
			text = result.Content
		}
		if strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("Genspark 返回内容为空")
		}
		completion := estimatePromptTokens(text)
		return jsonResponse(map[string]any{
			"id": id, "object": "chat.completion", "created": created, "model": model,
			"choices": []any{map[string]any{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": text},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{
				"prompt_tokens": promptTokens, "completion_tokens": completion, "total_tokens": promptTokens + completion,
			},
		}), nil
	}

	pr, pw := io.Pipe()
	header := http.Header{}
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")

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
		result := parseGensparkStream(resp.Body, func(d string) {
			writeChunk(map[string]any{"content": d}, nil)
			_ = bw.Flush()
		})
		if result.Deltas == "" && result.Content != "" {
			writeChunk(map[string]any{"content": result.Content}, nil)
		}
		writeChunk(map[string]any{}, "stop")
		completion := estimatePromptTokens(result.Deltas)
		if completion > 0 {
			raw, _ := json.Marshal(map[string]any{
				"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
				"choices": []any{},
				"usage":   map[string]any{"prompt_tokens": promptTokens, "completion_tokens": completion, "total_tokens": promptTokens + completion},
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

func gensparkPromptText(messages []map[string]any) string {
	var sb strings.Builder
	for _, m := range messages {
		sb.WriteString(messageText(m["content"]))
	}
	return sb.String()
}
