package main

import (
	"fmt"
	"strings"
)

const (
	ruleTypeMatch    = "match"
	ruleTypeInject   = "inject"
	ruleTypeOverride = "override"
	ruleTypeHeader   = "header"
	ruleTypeDeny     = "deny"
)

type RuleCond struct {
	Models       []string `json:"models,omitempty"`
	Paths        []string `json:"paths,omitempty"`
	HeaderKey    string   `json:"header_key,omitempty"`
	HeaderValue  string   `json:"header_value,omitempty"`
	BodyContains string   `json:"body_contains,omitempty"`
	Expr         string   `json:"expr,omitempty"`
}

type Rule struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	Disabled    bool           `json:"disabled,omitempty"`
	Cond        *RuleCond      `json:"condition,omitempty"`
	Instruction string         `json:"instruction,omitempty"`
	ProfileID   string         `json:"profile_id,omitempty"`
	SetModel    string         `json:"set_model,omitempty"`
	SetParams   map[string]any `json:"set_params,omitempty"`
	HeaderName  string         `json:"header_name,omitempty"`
	HeaderValue string         `json:"header_value,omitempty"`
	Status      int            `json:"status,omitempty"`
	Message     string         `json:"message,omitempty"`
}

func (r Rule) enabled() bool { return !r.Disabled }

type ruleContext struct {
	Model   string
	Path    string
	Body    string
	Headers map[string]string
}

type ruleOutcome struct {
	Denied        bool
	Status        int
	Message       string
	Instruction   string
	ModelOverride string
	Params        map[string]any
	Headers       map[string]string
	Matched       []string
	Skipped       []string
}

func (o ruleOutcome) hasInstruction() bool { return strings.TrimSpace(o.Instruction) != "" }

func condMatches(cond *RuleCond, ctx ruleContext) bool {
	if cond == nil {
		return true
	}
	checked := false
	if len(cond.Models) > 0 {
		checked = true
		hit := false
		for _, m := range cond.Models {
			if matchModelPattern(m, ctx.Model) || m == "*" {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if len(cond.Paths) > 0 {
		checked = true
		hit := false
		for _, p := range cond.Paths {
			if p == "" {
				continue
			}
			if ctx.Path == p || strings.HasPrefix(ctx.Path, p) || p == "*" {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if cond.HeaderKey != "" {
		checked = true
		val, ok := lookupHeader(ctx.Headers, cond.HeaderKey)
		if !ok {
			return false
		}
		if cond.HeaderValue != "" && !strings.Contains(val, cond.HeaderValue) {
			return false
		}
	}
	if cond.BodyContains != "" {
		checked = true
		if !strings.Contains(ctx.Body, cond.BodyContains) {
			return false
		}
	}
	if strings.TrimSpace(cond.Expr) != "" {
		checked = true
		ok, err := evalExpr(cond.Expr, ctx)
		if err != nil || !ok {
			return false
		}
	}
	_ = checked
	return true
}

func lookupHeader(headers map[string]string, key string) (string, bool) {
	if headers == nil {
		return "", false
	}
	if v, ok := headers[key]; ok {
		return v, true
	}
	lower := strings.ToLower(key)
	for k, v := range headers {
		if strings.ToLower(k) == lower {
			return v, true
		}
	}
	return "", false
}

func evaluateRules(rules []Rule, profiles []Profile, ctx ruleContext) ruleOutcome {
	out := ruleOutcome{}
	instructions := make([]string, 0, 2)

	for _, r := range rules {
		if !r.enabled() {
			continue
		}
		if !condMatches(r.Cond, ctx) {
			out.Skipped = append(out.Skipped, r.ID)
			continue
		}
		out.Matched = append(out.Matched, r.ID)

		switch r.Type {
		case ruleTypeInject:
			text := strings.TrimSpace(r.Instruction)
			if text == "" && r.ProfileID != "" {
				for _, p := range profiles {
					if p.ID == r.ProfileID {
						text = strings.TrimSpace(p.Instruction)
						break
					}
				}
			}
			if text != "" {
				instructions = append(instructions, text)
			}
		case ruleTypeOverride:
			if strings.TrimSpace(r.SetModel) != "" {
				out.ModelOverride = strings.TrimSpace(r.SetModel)
			}
			if len(r.SetParams) > 0 {
				if out.Params == nil {
					out.Params = map[string]any{}
				}
				for k, v := range r.SetParams {
					out.Params[k] = v
				}
			}
		case ruleTypeHeader:
			if strings.TrimSpace(r.HeaderName) != "" {
				if out.Headers == nil {
					out.Headers = map[string]string{}
				}
				out.Headers[r.HeaderName] = r.HeaderValue
			}
		case ruleTypeDeny:
			if len(instructions) > 0 {
				out.Instruction = strings.Join(instructions, "\n\n")
			}
			out.Denied = true
			out.Status = r.Status
			if out.Status == 0 {
				out.Status = 403
			}
			out.Message = r.Message
			if strings.TrimSpace(out.Message) == "" {
				out.Message = fmt.Sprintf("请求被规则 %s 拦截", r.Name)
			}
			return out
		case ruleTypeMatch:
		}
	}
	if len(instructions) > 0 {
		out.Instruction = strings.Join(instructions, "\n\n")
	}
	return out
}

func validateRuleCond(cond *RuleCond) error {
	if cond == nil || strings.TrimSpace(cond.Expr) == "" {
		return nil
	}
	_, err := evalExpr(cond.Expr, ruleContext{Model: "probe-model", Path: "/v1/chat/completions", Body: "{}"})
	return err
}

func validateRules(rules []Rule) error {
	for i, r := range rules {
		if strings.TrimSpace(r.ID) == "" {
			return fmt.Errorf("rules[%d]: id 不能为空", i)
		}
		switch r.Type {
		case ruleTypeMatch, ruleTypeInject, ruleTypeOverride, ruleTypeHeader, ruleTypeDeny:
		default:
			return fmt.Errorf("rules[%d] (%s): 未知类型 %q", i, r.ID, r.Type)
		}
		if err := validateRuleCond(r.Cond); err != nil {
			return fmt.Errorf("rules[%d] (%s): condition.expr 非法：%v", i, r.ID, err)
		}
		if r.Type == ruleTypeInject && strings.TrimSpace(r.Instruction) == "" && r.ProfileID == "" {
			return fmt.Errorf("rules[%d] (%s): inject 节点需要 instruction 或 profile_id", i, r.ID)
		}
		if r.Type == ruleTypeHeader && strings.TrimSpace(r.HeaderName) == "" {
			return fmt.Errorf("rules[%d] (%s): header 节点需要 header_name", i, r.ID)
		}
		if r.Type == ruleTypeDeny && r.Status != 0 && (r.Status < 400 || r.Status > 599) {
			return fmt.Errorf("rules[%d] (%s): deny 的 status 需要在 400-599", i, r.ID)
		}
	}
	return nil
}

func defaultRules() []Rule {
	return []Rule{
		{
			ID:       "r-deny-empty",
			Name:     "拦截空请求",
			Type:     ruleTypeDeny,
			Disabled: false,
			Cond:     &RuleCond{BodyContains: "\"messages\":[]"},
			Status:   400,
			Message:  "空 messages 请求已被规则拦截",
		},
	}
}
