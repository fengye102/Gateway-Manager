package main

import (
	"bytes"
	"encoding/json"
	"strings"
)

func usageFromMap(m map[string]any) (int, bool) {
	raw, ok := m["usage"]
	if !ok {
		if resp, ok2 := m["response"].(map[string]any); ok2 {
			raw, ok = resp["usage"]
		}
	}
	if !ok {
		return 0, false
	}
	usage, ok := raw.(map[string]any)
	if !ok || usage == nil {
		return 0, false
	}
	if total := jsonIntField(usage, "total_tokens"); total > 0 {
		return total, true
	}
	prompt := jsonIntField(usage, "prompt_tokens") + jsonIntField(usage, "input_tokens")
	completion := jsonIntField(usage, "completion_tokens") + jsonIntField(usage, "output_tokens")
	sum := prompt + completion
	if sum > 0 {
		return sum, true
	}
	return 0, false
}

// extractUsageTokens 同时支持普通 JSON 响应与 SSE 文本（取最后一处 usage）。
func extractUsageTokens(raw []byte) (int, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return 0, false
	}
	if trimmed[0] == '{' {
		payload := map[string]any{}
		if err := json.Unmarshal(trimmed, &payload); err == nil {
			if total, ok := usageFromMap(payload); ok {
				return total, true
			}
		}
	}
	best := 0
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payloadText := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payloadText == "" || payloadText == "[DONE]" {
			continue
		}
		payload := map[string]any{}
		if err := json.Unmarshal([]byte(payloadText), &payload); err != nil {
			continue
		}
		if total, ok := usageFromMap(payload); ok {
			best = total
			found = true
		}
	}
	return best, found
}

// usageSniffer 保留流式响应尾部窗口，结束后一次性解析 usage。
type usageSniffer struct {
	windowLimit int
	buf         []byte
}

func newUsageSniffer() *usageSniffer {
	return &usageSniffer{windowLimit: 256 * 1024}
}

func (s *usageSniffer) Write(p []byte) {
	s.buf = append(s.buf, p...)
	if len(s.buf) > s.windowLimit {
		s.buf = append([]byte(nil), s.buf[len(s.buf)-s.windowLimit:]...)
	}
}

func (s *usageSniffer) total() (int, bool) {
	return extractUsageTokens(s.buf)
}
