package main

// Anthropic ↔ OpenAI 方言翻译层。
//
// 网关对外同时暴露 OpenAI（/v1/chat/completions）与 Anthropic（/v1/messages）
// 两种协议，但 upstreams 绝大多数只讲 OpenAI 方言。此前 /v1/messages 的请求体
// 会被原样透传给上游的 /v1/messages，对 OpenAI-only 上游必然失败（403/404，
// 例如 sub2api 分组返回 "This group does not allow /v1/messages dispatch"）。
//
// 这里补上双向翻译：
//   请求：Anthropic messages 请求体 → OpenAI chat/completions 请求体
//   响应：OpenAI JSON / SSE → Anthropic message JSON / message 流事件
//
// 启用条件：upstream 未匹配到带显式 messages_path 的 adapter（即上游不是
// Anthropic 原生）时，epMessages 请求翻译为 epChat 转发；配置了 messages_path
// 的原生上游（如 api.anthropic.com adapter）仍走原样透传。

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// upstreamAnthropicNative 上游是否原生支持 Anthropic /v1/messages。
func upstreamAnthropicNative(cfg *Config, up *Upstream) bool {
	a := findAdapter(cfg, up.BaseURL)
	return a != nil && strings.TrimSpace(a.MessagesPath) != ""
}

// ── 请求翻译：Anthropic → OpenAI ─────────────────────────────────────────────

// anthropicRequestToOpenAI 把准备好的（已含 model 别名与策略注入的）Anthropic
// 请求体翻译成 OpenAI chat/completions 请求体。
func anthropicRequestToOpenAI(payload []byte) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	out := map[string]any{}
	for _, k := range []string{"model", "max_tokens", "temperature", "top_p", "stream", "user"} {
		if v, ok := req[k]; ok {
			out[k] = v
		}
	}
	if v, ok := req["stop_sequences"]; ok {
		out["stop"] = v
	}

	msgs := make([]any, 0, 16)
	if sys := anthropicTextValue(req["system"]); sys != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": sys})
	}
	raw, _ := req["messages"].([]any)
	for _, m := range raw {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		for _, conv := range anthropicMessageToOpenAI(role, msg["content"]) {
			msgs = append(msgs, conv)
		}
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("no translatable messages found")
	}
	out["messages"] = msgs

	if rawTools, ok := req["tools"].([]any); ok && len(rawTools) > 0 {
		tools := make([]any, 0, len(rawTools))
		for _, t := range rawTools {
			tm, ok := t.(map[string]any)
			if !ok {
				continue
			}
			fn := map[string]any{"name": tm["name"]}
			if d, ok := tm["description"]; ok {
				fn["description"] = d
			}
			if sc, ok := tm["input_schema"]; ok {
				fn["parameters"] = sc
			}
			tools = append(tools, map[string]any{"type": "function", "function": fn})
		}
		if len(tools) > 0 {
			out["tools"] = tools
		}
	}
	if tc, ok := req["tool_choice"].(map[string]any); ok {
		switch tc["type"] {
		case "auto":
			out["tool_choice"] = "auto"
		case "any":
			out["tool_choice"] = "required"
		case "tool":
			name, _ := tc["name"].(string)
			out["tool_choice"] = map[string]any{
				"type":     "function",
				"function": map[string]any{"name": name},
			}
		}
	}
	return json.Marshal(out)
}

// anthropicTextValue 把 Anthropic 的 system（string 或 text 块数组）拍平成 string。
func anthropicTextValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, b := range t {
			if bm, ok := b.(map[string]any); ok {
				if bt, _ := bm["type"].(string); bt == "text" {
					if txt, ok := bm["text"].(string); ok {
						parts = append(parts, txt)
					}
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// anthropicMessageToOpenAI 把单条 Anthropic message 翻译成 1..N 条 OpenAI message。
// user 消息里的 tool_result 块要拆成独立的 role:"tool" 消息；assistant 消息里的
// tool_use 块挂到 tool_calls 上。
func anthropicMessageToOpenAI(role string, content any) []map[string]any {
	textParts := make([]string, 0, 8)
	imageParts := make([]any, 0, 4)
	var toolCalls []any
	var toolResults []map[string]any

	appendBlock := func(b map[string]any) {
		switch bt, _ := b["type"].(string); bt {
		case "text":
			if txt, ok := b["text"].(string); ok {
				textParts = append(textParts, txt)
			}
		case "image":
			if part := anthropicImagePart(b["source"]); part != nil {
				imageParts = append(imageParts, part)
			}
		case "tool_use":
			input := map[string]any{}
			if raw, err := json.Marshal(b["input"]); err == nil && len(raw) > 0 && string(raw) != "null" {
				_ = json.Unmarshal(raw, &input)
			}
			args, _ := json.Marshal(input)
			id, _ := b["id"].(string)
			name, _ := b["name"].(string)
			toolCalls = append(toolCalls, map[string]any{
				"id":   id,
				"type": "function",
				"function": map[string]any{
					"name":      name,
					"arguments": string(args),
				},
			})
		case "tool_result":
			id, _ := b["tool_use_id"].(string)
			toolResults = append(toolResults, map[string]any{
				"role":         "tool",
				"tool_call_id": id,
				"content":      anthropicToolResultContent(b["content"]),
			})
		}
	}

	switch c := content.(type) {
	case string:
		if c != "" {
			textParts = append(textParts, c)
		}
	case []any:
		for _, blk := range c {
			if bm, ok := blk.(map[string]any); ok {
				appendBlock(bm)
			}
		}
	}

	out := make([]map[string]any, 0, 2)
	switch role {
	case "assistant":
		m := map[string]any{"role": "assistant"}
		if len(textParts) > 0 {
			m["content"] = strings.Join(textParts, "")
		} else {
			m["content"] = nil
		}
		if len(toolCalls) > 0 {
			m["tool_calls"] = toolCalls
		}
		out = append(out, m)
	default: // user 及其他一律按 user 处理
		// 按块顺序输出：tool_result 必须紧跟 assistant(tool_calls) 之后，
		// 其前的文本/图片先独立成 user 消息，其后的文本再开新 user 消息
		var pendingText []string
		var pendingImages []any
		flushPending := func() {
			if len(pendingImages) > 0 {
				parts := make([]any, 0, len(pendingImages)+1)
				if txt := strings.Join(pendingText, ""); txt != "" {
					parts = append(parts, map[string]any{"type": "text", "text": txt})
				}
				parts = append(parts, pendingImages...)
				out = append(out, map[string]any{"role": "user", "content": parts})
			} else if len(pendingText) > 0 {
				out = append(out, map[string]any{"role": "user", "content": strings.Join(pendingText, "")})
			}
			pendingText = nil
			pendingImages = nil
		}
		switch c := content.(type) {
		case string:
			if c != "" {
				pendingText = append(pendingText, c)
			}
		case []any:
			for _, blk := range c {
				bm, ok := blk.(map[string]any)
				if !ok {
					continue
				}
				switch bt, _ := bm["type"].(string); bt {
				case "text":
					if txt, ok := bm["text"].(string); ok {
						pendingText = append(pendingText, txt)
					}
				case "image":
					if part := anthropicImagePart(bm["source"]); part != nil {
						pendingImages = append(pendingImages, part)
					}
				case "tool_result":
					flushPending()
					id, _ := bm["tool_use_id"].(string)
					out = append(out, map[string]any{
						"role":         "tool",
						"tool_call_id": id,
						"content":      anthropicToolResultContent(bm["content"]),
					})
				}
			}
		}
		flushPending()
		if len(out) == 0 {
			out = append(out, map[string]any{"role": "user", "content": ""})
		}
	}
	return out
}

// anthropicImagePart 把 Anthropic image.source 转成 OpenAI 的 image_url part。
func anthropicImagePart(source any) any {
	src, ok := source.(map[string]any)
	if !ok {
		return nil
	}
	switch st, _ := src["type"].(string); st {
	case "base64":
		mt, _ := src["media_type"].(string)
		data, _ := src["data"].(string)
		if mt == "" || data == "" {
			return nil
		}
		return map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": "data:" + mt + ";base64," + data},
		}
	case "url":
		u, _ := src["url"].(string)
		if u == "" {
			return nil
		}
		return map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": u},
		}
	}
	return nil
}

// anthropicToolResultContent 把 tool_result 的 content（string 或块数组）拍平成字符串。
func anthropicToolResultContent(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, b := range t {
			if bm, ok := b.(map[string]any); ok {
				if bt, _ := bm["type"].(string); bt == "text" {
					if txt, ok := bm["text"].(string); ok {
						parts = append(parts, txt)
					}
				}
			} else if s, ok := b.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// ── 响应翻译：OpenAI → Anthropic ─────────────────────────────────────────────

func anthropicStopReason(finishReason string) any {
	switch finishReason {
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	case "content_filter":
		return "refusal"
	}
	return "end_turn"
}

// openAIMessageToAnthropic 把 OpenAI chat.completion 对象翻译成 Anthropic message。
func openAIMessageToAnthropic(resp map[string]any, fallbackModel string) map[string]any {
	msgID, _ := resp["id"].(string)
	if msgID != "" {
		msgID = "msg_" + msgID
	} else {
		msgID = "msg_translated"
	}
	model := fallbackModel
	if m, ok := resp["model"].(string); ok && m != "" {
		model = m
	}
	out := map[string]any{
		"id":            msgID,
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       []any{},
		"stop_reason":   nil,
		"stop_sequence": nil,
		"usage":         map[string]any{"input_tokens": 0, "output_tokens": 0},
	}
	if u, ok := resp["usage"].(map[string]any); ok {
		in, outt := anthropicUsageInts(u)
		out["usage"] = map[string]any{"input_tokens": in, "output_tokens": outt}
	}
	choices, _ := resp["choices"].([]any)
	if len(choices) == 0 {
		return out
	}
	ch, _ := choices[0].(map[string]any)
	content := []any{}
	if ch != nil {
		if msg, ok := ch["message"].(map[string]any); ok {
			if txt, ok := msg["content"].(string); ok && txt != "" {
				content = append(content, map[string]any{"type": "text", "text": txt})
			}
			if tcs, ok := msg["tool_calls"].([]any); ok {
				for _, tc := range tcs {
					m, ok := tc.(map[string]any)
					if !ok {
						continue
					}
					fn, _ := m["function"].(map[string]any)
					name, _ := fn["name"].(string)
					input := map[string]any{}
					if args, ok := fn["arguments"].(string); ok && strings.TrimSpace(args) != "" {
						_ = json.Unmarshal([]byte(args), &input)
					}
					id, _ := m["id"].(string)
					content = append(content, map[string]any{
						"type": "tool_use", "id": id, "name": name, "input": input,
					})
				}
			}
		}
		if fr, ok := ch["finish_reason"].(string); ok {
			out["stop_reason"] = anthropicStopReason(fr)
		}
	}
	out["content"] = content
	return out
}

// anthropicUsageInts 容忍 usage 数字以 float64 / int 形态出现。
func anthropicUsageInts(u map[string]any) (int, int) {
	in := anthropicUsageInt(u["prompt_tokens"])
	outt := anthropicUsageInt(u["completion_tokens"])
	return in, outt
}

func anthropicUsageInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

// anthropicErrorBody 把 OpenAI 风格错误体翻译成 Anthropic 错误形状。
func anthropicErrorBody(status int, body []byte) map[string]any {
	msg := strings.TrimSpace(string(body))
	typ := "api_error"
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err == nil {
		if e, ok := parsed["error"].(map[string]any); ok {
			if m, ok := e["message"].(string); ok && m != "" {
				msg = m
			}
			if t, ok := e["type"].(string); ok && t != "" {
				typ = t
			}
		}
	}
	switch {
	case status == 400:
		typ = "invalid_request_error"
	case status == 401:
		typ = "authentication_error"
	case status == 403:
		typ = "permission_error"
	case status == 404:
		typ = "not_found_error"
	case status == 429:
		typ = "rate_limit_error"
	case status >= 500:
		typ = "api_error"
	}
	return map[string]any{
		"type":  "error",
		"error": map[string]any{"type": typ, "message": msg},
	}
}

// ── 流式翻译：OpenAI SSE → Anthropic message 流 ──────────────────────────────

// anthropicStreamEncoder 维护块状态机，把逐条解析出的 OpenAI chunk 转成
// Anthropic message 流事件序列。Anthropic 的 content blocks 是串行的，
// 因此同一时刻只允许一个打开的块。
type anthropicStreamEncoder struct {
	write        func(string)
	model        string
	started      bool
	textOpen     bool
	toolOpen     bool
	toolStarted  bool // 本流里是否已出现过工具块
	toolCallIdx  int  // 当前打开块对应的 OpenAI tool_calls index
	toolBlockIdx int  // 当前打开工具块的 Anthropic block index
	nextIndex    int
	inputTokens  int
	outputTokens int
	stopReason   any
}

func (e *anthropicStreamEncoder) emit(event string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	e.write("event: " + event + "\ndata: " + string(raw) + "\n\n")
}

func (e *anthropicStreamEncoder) start(model string) {
	if e.started {
		return
	}
	e.started = true
	if model != "" {
		e.model = model
	}
	e.emit("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            "msg_stream",
			"type":          "message",
			"role":          "assistant",
			"model":         e.model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         map[string]any{"input_tokens": e.inputTokens, "output_tokens": 0},
		},
	})
}

func (e *anthropicStreamEncoder) closeOpenBlock() {
	if e.textOpen {
		e.emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": e.nextIndex - 1})
		e.textOpen = false
	}
	if e.toolOpen {
		e.emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": e.toolBlockIdx})
		e.toolOpen = false
	}
}

func (e *anthropicStreamEncoder) finish() {
	if !e.started {
		return
	}
	e.closeOpenBlock()
	reason := e.stopReason
	if reason == nil {
		reason = "end_turn"
	}
	e.emit("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": reason, "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": e.outputTokens},
	})
	e.emit("message_stop", map[string]any{"type": "message_stop"})
}

// handleChunk 处理单个 OpenAI chunk 对象。
func (e *anthropicStreamEncoder) handleChunk(chunk map[string]any) {
	if !e.started {
		if m, _ := chunk["model"].(string); m != "" {
			e.model = m
		}
		e.start(e.model)
	}
	if u, ok := chunk["usage"].(map[string]any); ok {
		in, out := anthropicUsageInts(u)
		if in > 0 {
			e.inputTokens = in
		}
		if out > 0 {
			e.outputTokens = out
		}
	}
	choices, _ := chunk["choices"].([]any)
	if len(choices) == 0 {
		return
	}
	ch, _ := choices[0].(map[string]any)
	if ch == nil {
		return
	}
	if fr, ok := ch["finish_reason"].(string); ok && fr != "" {
		e.stopReason = anthropicStopReason(fr)
	}
	delta, _ := ch["delta"].(map[string]any)
	if delta == nil {
		return
	}
	if txt, ok := delta["content"].(string); ok && txt != "" {
		if !e.textOpen {
			e.closeOpenBlock()
			idx := e.nextIndex
			e.nextIndex++
			e.emit("content_block_start", map[string]any{
				"type":          "content_block_start",
				"index":         idx,
				"content_block": map[string]any{"type": "text", "text": ""},
			})
			e.textOpen = true
		}
		e.emit("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": e.nextIndex - 1,
			"delta": map[string]any{"type": "text_delta", "text": txt},
		})
	}
	if tcs, ok := delta["tool_calls"].([]any); ok {
		for _, t := range tcs {
			m, ok := t.(map[string]any)
			if !ok {
				continue
			}
			oi := 0
			if v, ok := m["index"].(float64); ok {
				oi = int(v)
			}
			if !e.toolOpen || !e.toolStarted || oi != e.toolCallIdx {
				e.closeOpenBlock()
				bi := e.nextIndex
				e.nextIndex++
				e.toolBlockIdx = bi
				e.toolOpen = true
				e.toolStarted = true
				e.toolCallIdx = oi
				fn, _ := m["function"].(map[string]any)
				name, _ := fn["name"].(string)
				id, _ := m["id"].(string)
				e.emit("content_block_start", map[string]any{
					"type":          "content_block_start",
					"index":         bi,
					"content_block": map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{}},
				})
			}
			if fn, ok := m["function"].(map[string]any); ok {
				if args, ok := fn["arguments"].(string); ok && args != "" {
					e.emit("content_block_delta", map[string]any{
						"type":  "content_block_delta",
						"index": e.toolBlockIdx,
						"delta": map[string]any{"type": "input_json_delta", "partial_json": args},
					})
				}
			}
		}
	}
}

// streamAnthropicFromOpenAI 逐行读上游 OpenAI SSE，翻译成 Anthropic 流写出。
// 返回 (总 token 数, 是否拿到了 usage)。
func streamAnthropicFromOpenAI(w io.Writer, flush func(), body io.Reader, fallbackModel string) (int, bool) {
	enc := &anthropicStreamEncoder{
		write: func(s string) {
			_, _ = io.WriteString(w, s)
			if flush != nil {
				flush()
			}
		},
		model:        fallbackModel,
		toolBlockIdx: -1,
	}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		enc.handleChunk(chunk)
	}
	enc.finish()
	if enc.inputTokens+enc.outputTokens > 0 {
		return enc.inputTokens + enc.outputTokens, true
	}
	return 0, false
}

// relayAnthropicFromOpenAI 把 OpenAI 形状的上游响应翻译成 Anthropic 形状回写给客户端。
func (s *Server) relayAnthropicFromOpenAI(w http.ResponseWriter, resp *http.Response, wantStream bool) (int, bool) {
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_ = json.NewEncoder(w).Encode(anthropicErrorBody(resp.StatusCode, raw))
		return 0, false
	}
	cfg := s.store.Config()
	contentType := resp.Header.Get("Content-Type")
	isSSE := strings.Contains(strings.ToLower(contentType), "text/event-stream")
	flusher, _ := w.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}

	// 客户端不要流，上游给了流：合并后翻译成完整 message
	if isSSE && !wantStream && cfg.Server.MergeSSE {
		merged, err := mergeSSEChunks(resp.Body)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, anthropicErrorBody(http.StatusBadGateway,
				[]byte("合并上游 SSE 失败："+err.Error())))
			return 0, false
		}
		msg := openAIMessageToAnthropic(merged, "")
		writeJSON(w, http.StatusOK, msg)
		if u, ok := msg["usage"].(map[string]any); ok {
			in := anthropicUsageInt(u["input_tokens"])
			out := anthropicUsageInt(u["output_tokens"])
			if in+out > 0 {
				return in + out, true
			}
		}
		return 0, false
	}

	if isSSE {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		return streamAnthropicFromOpenAI(w, flush, resp.Body, "")
	}

	// 上游没流式返回：按完整 JSON 翻译
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, anthropicErrorBody(http.StatusBadGateway, []byte(err.Error())))
		return 0, false
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		writeJSON(w, http.StatusBadGateway, anthropicErrorBody(http.StatusBadGateway,
			[]byte("上游返回了非 JSON 的响应")))
		return 0, false
	}
	msg := openAIMessageToAnthropic(obj, "")
	writeJSON(w, http.StatusOK, msg)
	if u, ok := msg["usage"].(map[string]any); ok {
		in := anthropicUsageInt(u["input_tokens"])
		out := anthropicUsageInt(u["output_tokens"])
		if in+out > 0 {
			return in + out, true
		}
	}
	return 0, false
}
