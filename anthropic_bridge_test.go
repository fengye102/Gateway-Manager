package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnthropicRequestTranslation(t *testing.T) {
	anthropic := `{
	  "model": "glm-5.3",
	  "max_tokens": 256,
	  "temperature": 0.5,
	  "top_p": 0.9,
	  "stream": false,
	  "stop_sequences": ["END", "STOP"],
	  "system": [{"type": "text", "text": "你是严谨的工程师"}, {"type": "text", "text": "输出中文"}],
	  "messages": [
	    {"role": "user", "content": [
	      {"type": "text", "text": "看这张图"},
	      {"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "aWNvbg=="}}
	    ]},
	    {"role": "assistant", "content": [
	      {"type": "text", "text": "我看到了图标"},
	      {"type": "tool_use", "id": "toolu_1", "name": "get_size", "input": {"unit": "px"}}
	    ]},
	    {"role": "user", "content": [
	      {"type": "tool_result", "tool_use_id": "toolu_1", "content": [{"type": "text", "text": "32"}]},
	      {"type": "text", "text": "谢谢"}
	    ]}
	  ],
	  "tools": [
	    {"name": "get_size", "description": "取尺寸", "input_schema": {"type": "object", "properties": {"unit": {"type": "string"}}}}
	  ],
	  "tool_choice": {"type": "auto"}
	}`
	out, err := anthropicRequestToOpenAI([]byte(anthropic))
	if err != nil {
		t.Fatalf("翻译失败: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("输出不是合法 JSON: %v", err)
	}

	if got["model"] != "glm-5.3" || got["max_tokens"] != float64(256) {
		t.Errorf("透传字段错误: model=%v max_tokens=%v", got["model"], got["max_tokens"])
	}
	if got["stop"] == nil {
		t.Errorf("stop_sequences 应映射为 stop")
	}
	msgs, _ := got["messages"].([]any)
	// system + user(图) + assistant + user文本 + tool = 5 条
	if len(msgs) != 5 {
		t.Fatalf("消息条数应为 5，实际 %d: %v", len(msgs), got["messages"])
	}
	if m0, _ := msgs[0].(map[string]any); m0["role"] != "system" || m0["content"] != "你是严谨的工程师\n输出中文" {
		t.Errorf("system 翻译错误: %v", m0)
	}
	m1, _ := msgs[1].(map[string]any)
	parts, _ := m1["content"].([]any)
	if m1["role"] != "user" || len(parts) != 2 {
		t.Fatalf("图文 user 消息翻译错误: %v", m1)
	}
	img, _ := parts[1].(map[string]any)
	iu, _ := img["image_url"].(map[string]any)
	if img["type"] != "image_url" || iu["url"] != "data:image/png;base64,aWNvbg==" {
		t.Errorf("图片块翻译错误: %v", img)
	}
	m2, _ := msgs[2].(map[string]any)
	if m2["role"] != "assistant" || m2["content"] != "我看到了图标" {
		t.Errorf("assistant 文本翻译错误: %v", m2)
	}
	tcs, _ := m2["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("tool_use 应映射为 tool_calls: %v", m2)
	}
	tc0, _ := tcs[0].(map[string]any)
	fn, _ := tc0["function"].(map[string]any)
	if fn["name"] != "get_size" || fn["arguments"] != `{"unit":"px"}` {
		t.Errorf("tool_calls 内容错误: %v", tc0)
	}
	m3, _ := msgs[3].(map[string]any)
	if m3["role"] != "tool" || m3["tool_call_id"] != "toolu_1" || m3["content"] != "32" {
		t.Errorf("tool_result 应拆为 role:tool 消息: %v", m3)
	}
	m4, _ := msgs[4].(map[string]any)
	if m4["role"] != "user" || m4["content"] != "谢谢" {
		t.Errorf("tool_result 后的文本应独立成 user 消息: %v", m4)
	}
	tools, _ := got["tools"].([]any)
	t0, _ := tools[0].(map[string]any)
	if t0["type"] != "function" {
		t.Errorf("tools 应映射为 function 类型: %v", t0)
	}
	if got["tool_choice"] != "auto" {
		t.Errorf("tool_choice auto 映射错误: %v", got["tool_choice"])
	}
}

func TestOpenAIToAnthropicMessageTranslation(t *testing.T) {
	openai := map[string]any{
		"id":     "chatcmpl-9",
		"model":  "glm-5.3",
		"object": "chat.completion",
		"choices": []any{map[string]any{
			"index":         0,
			"finish_reason": "tool_calls",
			"message": map[string]any{
				"role":    "assistant",
				"content": "我来查一下",
				"tool_calls": []any{map[string]any{
					"id":   "call_1",
					"type": "function",
					"function": map[string]any{
						"name":      "get_weather",
						"arguments": `{"city":"武汉"}`,
					},
				}},
			},
		}},
		"usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 7},
	}
	msg := openAIMessageToAnthropic(openai, "")
	if msg["type"] != "message" || msg["role"] != "assistant" {
		t.Errorf("message 形状错误: %v", msg)
	}
	if msg["id"] != "msg_chatcmpl-9" {
		t.Errorf("id 应加 msg_ 前缀: %v", msg["id"])
	}
	if msg["stop_reason"] != "tool_use" {
		t.Errorf("finish_reason=tool_calls 应映射为 tool_use: %v", msg["stop_reason"])
	}
	usage, _ := msg["usage"].(map[string]any)
	if usage["input_tokens"] != 11 || usage["output_tokens"] != 7 {
		t.Errorf("usage 映射错误: %v", usage)
	}
	blocks, _ := msg["content"].([]any)
	if len(blocks) != 2 {
		t.Fatalf("应有 text + tool_use 两个块: %v", blocks)
	}
	if b0, _ := blocks[0].(map[string]any); b0["type"] != "text" || b0["text"] != "我来查一下" {
		t.Errorf("text 块错误: %v", b0)
	}
	if b1, _ := blocks[1].(map[string]any); b1["type"] != "tool_use" || b1["name"] != "get_weather" {
		t.Errorf("tool_use 块错误: %v", b1)
	} else if input, _ := b1["input"].(map[string]any); input["city"] != "武汉" {
		t.Errorf("tool_use input 解析错误: %v", input)
	}
}

func collectAnthropicEvents(t *testing.T, upstreamSSERaw string) string {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("翻译后应请求 chat/completions，实际 %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, upstreamSSERaw)
	}))
	t.Cleanup(upstream.Close)

	cfg := DefaultConfig()
	cfg.Server.Bind = "127.0.0.1"
	cfg.Server.AdminToken = ""
	cfg.Server.AccessKeys = nil
	cfg.Upstreams = []Upstream{{
		ID: "up1", Name: "测试上游", BaseURL: upstream.URL,
		Models: []string{"glm-5.3"}, Enabled: true,
	}}
	ts := startGateway(t, cfg)

	body := `{"model":"glm-5.3","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"数到3"}]}`
	resp, raw := postJSON(t, ts.URL+"/v1/messages", body, map[string]string{
		"anthropic-version": "2023-06-01",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d: %s", resp.StatusCode, raw)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("客户端应收到 SSE，实际 Content-Type %s", ct)
	}
	return raw
}

// sseLine 把任意 chunk 程序化转成 SSE 行，避免手搓 JSON 括号错误。
func sseLine(v any) string {
	b, _ := json.Marshal(v)
	return "data: " + string(b) + "\n\n"
}

// parseAnthropicEvents 把网关输出的 SSE 文本解析成 (event, data) 列表做结构化断言。
func parseAnthropicEvents(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, blk := range strings.Split(raw, "\n\n") {
		blk = strings.TrimSpace(blk)
		if blk == "" {
			continue
		}
		var event string
		var dataStr string
		for _, line := range strings.Split(blk, "\n") {
			if strings.HasPrefix(line, "event: ") {
				event = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				dataStr = strings.TrimPrefix(line, "data: ")
			}
		}
		if event == "" || dataStr == "" {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(dataStr), &data); err != nil {
			t.Fatalf("SSE data 不是合法 JSON: %v\n%s", err, dataStr)
		}
		data["_event"] = event
		out = append(out, data)
	}
	return out
}

func TestAnthropicStreamTranslationE2E(t *testing.T) {
	sse := sseLine(map[string]any{"id": "c1", "model": "glm-5.3", "choices": []any{map[string]any{
		"index": 0, "delta": map[string]any{"role": "assistant", "content": "1"}}}}) +
		sseLine(map[string]any{"id": "c1", "choices": []any{map[string]any{
			"index": 0, "delta": map[string]any{"content": "2 3"}}}}) +
		sseLine(map[string]any{"id": "c1", "choices": []any{map[string]any{
			"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
			"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 3}}) +
		"data: [DONE]\n\n"
	raw := collectAnthropicEvents(t, sse)
	events := parseAnthropicEvents(t, raw)

	if len(events) == 0 {
		t.Fatalf("未解析到任何事件:\n%s", raw)
	}
	if events[0]["_event"] != "message_start" {
		t.Errorf("第一个事件应为 message_start，实际 %v", events[0]["_event"])
	}
	startMsg, _ := events[0]["message"].(map[string]any)
	if startMsg == nil || startMsg["model"] != "glm-5.3" {
		t.Errorf("message_start 里应有模型名: %v", events[0])
	}
	var textDeltas []string
	var stopReason any
	hasStop, hasMessageStop := false, false
	for _, e := range events {
		switch e["_event"] {
		case "content_block_delta":
			if d, _ := e["delta"].(map[string]any); d != nil && d["type"] == "text_delta" {
				textDeltas = append(textDeltas, d["text"].(string))
			}
		case "message_delta":
			hasStop = true
			if d, _ := e["delta"].(map[string]any); d != nil {
				stopReason = d["stop_reason"]
			}
			if u, _ := e["usage"].(map[string]any); u != nil && u["output_tokens"] != float64(3) {
				t.Errorf("output_tokens 应为 3: %v", u)
			}
		case "message_stop":
			hasMessageStop = true
		}
	}
	if strings.Join(textDeltas, "") != "12 3" {
		t.Errorf("text_delta 拼接应为 12 3，实际 %q", textDeltas)
	}
	if !hasStop || stopReason != "end_turn" {
		t.Errorf("应有 message_delta 且 stop_reason=end_turn: %v", stopReason)
	}
	if !hasMessageStop {
		t.Errorf("缺少 message_stop 事件")
	}
	// 块必须成对关闭
	starts, stops := 0, 0
	for _, e := range events {
		switch e["_event"] {
		case "content_block_start":
			starts++
		case "content_block_stop":
			stops++
		}
	}
	if starts != stops {
		t.Errorf("content_block_start/stop 应成对: %d/%d", starts, stops)
	}
}

func TestAnthropicToolStreamTranslationE2E(t *testing.T) {
	sse := sseLine(map[string]any{"id": "c2", "model": "glm-5.3", "choices": []any{map[string]any{
		"index": 0, "delta": map[string]any{"content": "查天气"}}}}) +
		sseLine(map[string]any{"id": "c2", "choices": []any{map[string]any{
			"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": "call_9", "type": "function",
				"function": map[string]any{"name": "get_weather", "arguments": ""}}}}}}}) +
		sseLine(map[string]any{"id": "c2", "choices": []any{map[string]any{
			"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "function": map[string]any{"arguments": `{"city":`}}}}}}}) +
		sseLine(map[string]any{"id": "c2", "choices": []any{map[string]any{
			"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "function": map[string]any{"arguments": `"武汉"}`}}}},
			"finish_reason": "tool_calls"}}}) +
		"data: [DONE]\n\n"
	raw := collectAnthropicEvents(t, sse)
	events := parseAnthropicEvents(t, raw)

	var toolStart map[string]any
	var partials []string
	var stopReason any
	var textClosedBeforeTool bool
	var textStopIdx, toolStartIdx = -1, -1
	idx := 0
	for _, e := range events {
		switch e["_event"] {
		case "content_block_start":
			cb, _ := e["content_block"].(map[string]any)
			if cb != nil && cb["type"] == "tool_use" {
				toolStart = cb
				toolStartIdx = idx
			}
		case "content_block_stop":
			if textStopIdx == -1 {
				textStopIdx = idx
			}
		case "content_block_delta":
			if d, _ := e["delta"].(map[string]any); d != nil && d["type"] == "input_json_delta" {
				partials = append(partials, d["partial_json"].(string))
			}
		case "message_delta":
			if d, _ := e["delta"].(map[string]any); d != nil {
				stopReason = d["stop_reason"]
			}
		}
		idx++
	}
	if toolStart == nil || toolStart["id"] != "call_9" || toolStart["name"] != "get_weather" {
		t.Errorf("tool_use 块开始事件错误: %v", toolStart)
	}
	if strings.Join(partials, "") != `{"city":"武汉"}` {
		t.Errorf("input_json_delta 拼接错误: %q", partials)
	}
	if stopReason != "tool_use" {
		t.Errorf("stop_reason 应为 tool_use: %v", stopReason)
	}
	if textStopIdx != -1 && toolStartIdx != -1 && textStopIdx > toolStartIdx {
		t.Errorf("text 块应先于 tool 块关闭\n%s", raw)
	}
	_ = textClosedBeforeTool
}

func TestMessagesEndpointNonStreamE2E(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"cc-1","object":"chat.completion","model":"glm-5.3","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"收到"}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
	}))
	t.Cleanup(upstream.Close)

	cfg := DefaultConfig()
	cfg.Server.Bind = "127.0.0.1"
	cfg.Server.AdminToken = ""
	cfg.Server.AccessKeys = nil
	cfg.Upstreams = []Upstream{{
		ID: "up1", Name: "测试上游", BaseURL: upstream.URL,
		Models: []string{"glm-5.3"}, Enabled: true,
	}}
	ts := startGateway(t, cfg)

	body := `{"model":"glm-5.3","max_tokens":32,"system":"你是测试","messages":[{"role":"user","content":"只回复两个字：收到"}]}`
	resp, raw := postJSON(t, ts.URL+"/v1/messages", body, map[string]string{
		"anthropic-version": "2023-06-01",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d: %s", resp.StatusCode, raw)
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("上游收到的应是翻译后的 chat/completions，实际 %s", gotPath)
	}
	sysMsg, _ := gotBody["messages"].([]any)
	if len(sysMsg) == 0 {
		t.Fatalf("上游收到的消息为空")
	}
	if m0, _ := sysMsg[0].(map[string]any); m0["role"] != "system" || m0["content"] != "你是测试" {
		t.Errorf("system 未翻译成 system 消息: %v", m0)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", raw)
	}
	if out["type"] != "message" || out["role"] != "assistant" {
		t.Errorf("响应应为 Anthropic message 形状: %v", out)
	}
	if out["stop_reason"] != "end_turn" {
		t.Errorf("stop_reason 应为 end_turn: %v", out["stop_reason"])
	}
	blocks, _ := out["content"].([]any)
	if len(blocks) != 1 {
		t.Fatalf("应有一个 content 块: %v", blocks)
	}
	if b, _ := blocks[0].(map[string]any); b["type"] != "text" || b["text"] != "收到" {
		t.Errorf("text 内容错误: %v", b)
	}
}

func TestMessagesPassthroughForNativeUpstream(t *testing.T) {
	// 显式配置了 messages_path 的 adapter：保持原样透传，不做翻译
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	t.Cleanup(upstream.Close)

	cfg := DefaultConfig()
	cfg.Server.Bind = "127.0.0.1"
	cfg.Server.AdminToken = ""
	cfg.Server.AccessKeys = nil
	cfg.Upstreams = []Upstream{{
		ID: "native", Name: "Anthropic 原生", BaseURL: upstream.URL,
		Models: []string{"claude-x"}, Enabled: true,
	}}
	cfg.Adapters = []Adapter{{
		ID: "native-adapter", MatchBaseURL: upstream.URL, MessagesPath: "/v1/messages",
	}}
	ts := startGateway(t, cfg)

	body := `{"model":"claude-x","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`
	resp, raw := postJSON(t, ts.URL+"/v1/messages", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d: %s", resp.StatusCode, raw)
	}
	if gotPath != "/v1/messages" {
		t.Errorf("原生上游应收到 /v1/messages 透传，实际 %s", gotPath)
	}
	if !strings.Contains(raw, `"type":"message"`) {
		t.Errorf("透传响应应保持 Anthropic 形状: %s", raw)
	}
}
