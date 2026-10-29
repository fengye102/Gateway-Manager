package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// 规则书 DSL：每行一条规则，when 是条件，action 是动作，与拖拽 UI 双向同步。
func rulesToDSL(rules []Rule) string {
	var b strings.Builder
	b.WriteString("# pojiagw 规则书（与拖拽 UI 双向同步）\n")
	b.WriteString("# 顶层字段：id / name / type / disabled\n")
	b.WriteString("# when：models / paths / header: Key=Value / body_contains\n")
	b.WriteString("# action：instruction / profile_id / model / params / header_name / header_value / status / message\n\n")
	for _, r := range rules {
		b.WriteString("- id: " + r.ID + "\n")
		if r.Name != "" {
			b.WriteString("  name: " + r.Name + "\n")
		}
		b.WriteString("  type: " + r.Type + "\n")
		if r.Disabled {
			b.WriteString("  disabled: true\n")
		}
		if c := r.Cond; c != nil {
			b.WriteString("  when:\n")
			if len(c.Models) > 0 {
				b.WriteString("    models: " + strings.Join(c.Models, ", ") + "\n")
			}
			if len(c.Paths) > 0 {
				b.WriteString("    paths: " + strings.Join(c.Paths, ", ") + "\n")
			}
			if c.HeaderKey != "" {
				header := c.HeaderKey
				if c.HeaderValue != "" {
					header += "=" + c.HeaderValue
				}
				b.WriteString("    header: " + header + "\n")
			}
			if c.BodyContains != "" {
				b.WriteString("    body_contains: " + c.BodyContains + "\n")
			}
		}
		hasAction := r.Instruction != "" || r.ProfileID != "" || r.SetModel != "" || len(r.SetParams) > 0 ||
			r.HeaderName != "" || r.Status != 0 || r.Message != ""
		if hasAction {
			b.WriteString("  action:\n")
			if r.Instruction != "" {
				b.WriteString("    instruction: " + r.Instruction + "\n")
			}
			if r.ProfileID != "" {
				b.WriteString("    profile_id: " + r.ProfileID + "\n")
			}
			if r.SetModel != "" {
				b.WriteString("    model: " + r.SetModel + "\n")
			}
			if len(r.SetParams) > 0 {
				if raw, err := json.Marshal(r.SetParams); err == nil {
					b.WriteString("    params: " + string(raw) + "\n")
				}
			}
			if r.HeaderName != "" {
				b.WriteString("    header_name: " + r.HeaderName + "\n")
			}
			if r.HeaderValue != "" {
				b.WriteString("    header_value: " + r.HeaderValue + "\n")
			}
			if r.Status != 0 {
				b.WriteString("    status: " + strconv.Itoa(r.Status) + "\n")
			}
			if r.Message != "" {
				b.WriteString("    message: " + r.Message + "\n")
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func parseRulesDSL(text string) ([]Rule, error) {
	rules := make([]Rule, 0, 8)
	var cur *Rule
	section := ""

	flush := func() {
		if cur != nil {
			rules = append(rules, *cur)
			cur = nil
		}
	}

	lines := strings.Split(text, "\n")
	for idx, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
			flush()
			cur = &Rule{}
			section = ""
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			if rest == "" {
				continue
			}
			trimmed = rest
			indent = 2
		}
		if cur == nil {
			return nil, fmt.Errorf("第 %d 行：规则必须以 \"- id: xxx\" 开头", idx+1)
		}

		key, value, ok := splitDSLKV(trimmed)
		if !ok {
			return nil, fmt.Errorf("第 %d 行：无法解析 %q（需要 key: value）", idx+1, trimmed)
		}
		key = strings.ToLower(key)
		if key == "when" || key == "action" {
			section = key
			continue
		}
		if indent <= 2 {
			section = ""
		}

		var err error
		switch section {
		case "when":
			err = applyWhen(cur, key, value)
		case "action":
			err = applyAction(cur, key, value)
		default:
			err = applyTop(cur, key, value)
		}
		if err != nil {
			return nil, fmt.Errorf("第 %d 行：%v", idx+1, err)
		}
	}
	flush()

	if len(rules) == 0 {
		return nil, fmt.Errorf("没有解析到任何规则")
	}
	return rules, nil
}

func splitDSLKV(line string) (string, string, bool) {
	idx := strings.Index(line, ":")
	if idx <= 0 {
		return "", "", false
	}
	key := strings.TrimSpace(line[:idx])
	value := strings.TrimSpace(line[idx+1:])
	value = strings.Trim(value, "\"")
	return key, value, true
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func applyTop(r *Rule, key string, value string) error {
	switch key {
	case "id":
		r.ID = value
	case "name":
		r.Name = value
	case "type":
		r.Type = value
	case "disabled":
		r.Disabled = value == "true" || value == "yes" || value == "1"
	default:
		return fmt.Errorf("未知顶层字段 %q", key)
	}
	return nil
}

func applyWhen(r *Rule, key string, value string) error {
	if r.Cond == nil {
		r.Cond = &RuleCond{}
	}
	switch key {
	case "models":
		r.Cond.Models = splitCSV(value)
	case "paths":
		r.Cond.Paths = splitCSV(value)
	case "header":
		parts := strings.SplitN(value, "=", 2)
		r.Cond.HeaderKey = strings.TrimSpace(parts[0])
		if len(parts) == 2 {
			r.Cond.HeaderValue = strings.TrimSpace(parts[1])
		}
	case "header_key":
		r.Cond.HeaderKey = value
	case "header_value":
		r.Cond.HeaderValue = value
	case "body_contains":
		r.Cond.BodyContains = value
	case "expr":
		r.Cond.Expr = value
	default:
		return fmt.Errorf("when 里未知字段 %q", key)
	}
	return nil
}

func applyAction(r *Rule, key string, value string) error {
	switch key {
	case "instruction":
		r.Instruction = value
	case "profile_id":
		r.ProfileID = value
	case "model":
		r.SetModel = value
	case "params":
		params := map[string]any{}
		if err := json.Unmarshal([]byte(value), &params); err != nil {
			return fmt.Errorf("params 不是合法 JSON：%v", err)
		}
		r.SetParams = params
	case "header_name":
		r.HeaderName = value
	case "header_value":
		r.HeaderValue = value
	case "status":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("status 需要数字")
		}
		r.Status = n
	case "message":
		r.Message = value
	default:
		return fmt.Errorf("action 里未知字段 %q", key)
	}
	return nil
}
