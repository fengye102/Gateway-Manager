package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// deepseekForward 把 OpenAI 请求转成 DeepSeek 网页协议，再把他们的 SSE 还原成 OpenAI 格式
func (s *Server) deepseekForward(ctx context.Context, ch Channel, preset ChannelPreset, payload []byte) (*http.Response, error) {
	var req map[string]any
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, fmt.Errorf("请求体不是 JSON：%v", err)
	}
	model, _ := req["model"].(string)
	stream := jsonBoolField(req, "stream")
	messages := extractMessages(req)
	if len(messages) == 0 {
		return nil, fmt.Errorf("messages 为空")
	}
	thinking := lenientBool(req["thinking"])
	search := lenientBool(req["search"])

	cookie := strings.TrimSpace(ch.Cookie)
	token := strings.TrimSpace(ch.Token)
	if strings.Contains(token, "=") && strings.Contains(token, "ds_session_id") {
		cookie, token = token, ""
	}

	client := newDeepseekClient(token, cookie, 180*time.Second)
	sessionID, err := client.createSession(ctx)
	if err != nil {
		return nil, err
	}
	prompt := buildDeepSeekPrompt(messages)
	promptTokens := estimatePromptTokens(prompt)
	vendorResp, err := client.complete(ctx, sessionID, prompt, thinking, search)
	if err != nil {
		return nil, err
	}
	if vendorResp.StatusCode < 200 || vendorResp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(vendorResp.Body, 4096))
		vendorResp.Body.Close()
		return nil, fmt.Errorf("DeepSeek 返回 HTTP %d：%s", vendorResp.StatusCode, truncateForReport(string(body), 300))
	}

	id := "chatcmpl-ds-" + strconv.FormatInt(time.Now().UnixMilli(), 10)
	created := time.Now().Unix()

	if !stream {
		defer vendorResp.Body.Close()
		var sb strings.Builder
		usage := parseDeepSeekStream(vendorResp.Body, func(delta string) { sb.WriteString(delta) })
		return jsonResponse(map[string]any{
			"id":      id,
			"object":  "chat.completion",
			"created": created,
			"model":   model,
			"choices": []any{map[string]any{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": sb.String()},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{
				"prompt_tokens":     promptTokens,
				"completion_tokens": usage,
				"total_tokens":      promptTokens + usage,
			},
		}), nil
	}

	pr, pw := io.Pipe()
	header := http.Header{}
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")

	go func() {
		defer vendorResp.Body.Close()
		defer pw.Close()
		bw := bufio.NewWriter(pw)
		writeChunk := func(delta map[string]any, finish any) {
			payloadObj := map[string]any{
				"id":      id,
				"object":  "chat.completion.chunk",
				"created": created,
				"model":   model,
				"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
			}
			raw, _ := json.Marshal(payloadObj)
			_, _ = bw.WriteString("data: ")
			_, _ = bw.Write(raw)
			_, _ = bw.WriteString("\n\n")
		}
		writeChunk(map[string]any{"role": "assistant", "content": ""}, nil)
		usage := parseDeepSeekStream(vendorResp.Body, func(delta string) {
			if delta == "" {
				return
			}
			writeChunk(map[string]any{"content": delta}, nil)
			if bw.Buffered() > 0 {
				_ = bw.Flush()
			}
		})
		writeChunk(map[string]any{}, "stop")
		if usage > 0 {
			raw, _ := json.Marshal(map[string]any{
				"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
				"choices": []any{},
				"usage":   map[string]any{"prompt_tokens": promptTokens, "completion_tokens": usage, "total_tokens": promptTokens + usage},
			})
			_, _ = bw.WriteString("data: ")
			_, _ = bw.Write(raw)
			_, _ = bw.WriteString("\n\n")
		}
		_, _ = bw.WriteString("data: [DONE]\n\n")
		_ = bw.Flush()
	}()

	return &http.Response{
		StatusCode: 200,
		Header:     header,
		Body:       pr,
	}, nil
}

// parseDeepSeekStream 解析 DeepSeek 的 patch 流，返回累计 token
func parseDeepSeekStream(r io.Reader, onDelta func(string)) int {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	usage := 0
	emitted := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "event:") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var patch map[string]any
		if err := json.Unmarshal([]byte(data), &patch); err != nil {
			continue
		}
		if p, _ := patch["p"].(string); p != "" {
			op, _ := patch["o"].(string)
			switch {
			case op == "APPEND":
				if s, ok := patch["v"].(string); ok && s != "" && emitted {
					onDelta(s)
				}
			case op == "BATCH":
				if items, ok := patch["v"].([]any); ok {
					for _, item := range items {
						m, _ := item.(map[string]any)
						if m == nil {
							continue
						}
						if key, _ := m["p"].(string); key == "accumulated_token_usage" {
							if n, ok := m["v"].(float64); ok {
								usage = int(n)
							}
						}
					}
				}
			}
			continue
		}
		v, ok := patch["v"]
		if !ok {
			continue
		}
		switch val := v.(type) {
		case string:
			if val != "" && emitted {
				onDelta(val)
			}
		case map[string]any:
			resp, _ := val["response"].(map[string]any)
			if resp == nil {
				continue
			}
			if u, ok := resp["accumulated_token_usage"].(float64); ok {
				usage = int(u)
			}
			if frags, ok := resp["fragments"].([]any); ok && !emitted {
				var sb strings.Builder
				for _, f := range frags {
					fm, _ := f.(map[string]any)
					if fm == nil {
						continue
					}
					if t, _ := fm["type"].(string); t != "" && t != "RESPONSE" {
						continue
					}
					if c, ok := fm["content"].(string); ok {
						sb.WriteString(c)
					}
				}
				emitted = true
				if sb.Len() > 0 {
					onDelta(sb.String())
				}
			} else if !emitted {
				emitted = true
			}
		}
	}
	return usage
}

func extractMessages(req map[string]any) []map[string]any {
	raw, ok := req["messages"].([]any)
	if !ok {
		if input, ok2 := req["input"].([]any); ok2 {
			raw = input
		}
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		if role == "" {
			role = "user"
		}
		out = append(out, map[string]any{"role": role, "content": m["content"]})
	}
	return out
}

func lenientBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(t, "true") || t == "1"
	case float64:
		return t != 0
	}
	return false
}

func jsonResponse(payload map[string]any) *http.Response {
	raw, _ := json.Marshal(payload)
	header := http.Header{}
	header.Set("Content-Type", "application/json; charset=utf-8")
	return &http.Response{
		StatusCode: 200,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(string(raw))),
	}
}

func estimatePromptTokens(text string) int {
	cjk, ascii := 0, 0
	for _, r := range text {
		switch {
		case r >= 0x2E80:
			cjk++
		case r > ' ':
			ascii++
		}
	}
	if cjk == 0 && ascii == 0 {
		return 0
	}
	return (cjk*2+ascii+3)/4 + 1
}
