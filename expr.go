package main

import (
	"fmt"
	"regexp"
	"strings"
)

// 支持：model == "x" / model != "x" / model ~ "正则" / model !~ "正则"
//
//	path ~ "/v1/messages" / body contains "text" / header:X-Client == "android"
//
// 组合：and（优先级高）/ or；单目 ! 取反；不支持括号。
func evalExpr(expr string, ctx ruleContext) (bool, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return true, nil
	}
	if strings.HasPrefix(expr, "(") && strings.HasSuffix(expr, ")") && balancedParens(expr) {
		return evalExpr(expr[1:len(expr)-1], ctx)
	}
	if strings.Contains(expr, "~~") || strings.Contains(expr, "==") && strings.Contains(expr, "===") {
		return false, fmt.Errorf("运算符重复：%q", expr)
	}
	if parts := splitTopLevel(expr, " or "); len(parts) > 1 {
		for _, part := range parts {
			ok, err := evalExpr(part, ctx)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	}
	if parts := splitTopLevel(expr, " and "); len(parts) > 1 {
		for _, part := range parts {
			ok, err := evalExpr(part, ctx)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	}
	return evalAtom(expr, ctx)
}

func balancedParens(expr string) bool {
	depth := 0
	quote := byte(0)
	for i := 0; i < len(expr); i++ {
		c := expr[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', 0x27:
			quote = c
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return false
			}
			if depth == 0 && i != len(expr)-1 {
				return false
			}
		}
	}
	return depth == 0
}

func splitTopLevel(expr string, sep string) []string {
	parts := make([]string, 0, 2)
	lower := strings.ToLower(expr)
	depthQuote := byte(0)
	start := 0
	for i := 0; i < len(expr); i++ {
		c := expr[i]
		if depthQuote != 0 {
			if c == depthQuote {
				depthQuote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			depthQuote = c
			continue
		}
		if strings.HasPrefix(lower[i:], sep) {
			parts = append(parts, expr[start:i])
			start = i + len(sep)
			i += len(sep) - 1
		}
	}
	parts = append(parts, expr[start:])
	return parts
}

func trimQuotes(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

func evalAtom(atom string, ctx ruleContext) (bool, error) {
	atom = strings.TrimSpace(atom)
	negate := false
	for strings.HasPrefix(atom, "!") && !strings.HasPrefix(atom, "!=") && !strings.HasPrefix(atom, "!~") {
		negate = !negate
		atom = strings.TrimSpace(atom[1:])
	}
	if atom == "" {
		return false, fmt.Errorf("空条件")
	}
	if strings.HasPrefix(atom, "(") && strings.HasSuffix(atom, ")") && balancedParens(atom) {
		inner, err := evalExpr(atom[1:len(atom)-1], ctx)
		if err != nil {
			return false, err
		}
		if negate {
			inner = !inner
		}
		return inner, nil
	}

	var field, op, value string
	if idx := strings.Index(strings.ToLower(atom), " contains "); idx > 0 {
		field = strings.TrimSpace(atom[:idx])
		op = "contains"
		value = atom[idx+len(" contains "):]
	} else {
		for _, candidate := range []string{"!~", "!=", "==", "~"} {
			if idx := strings.Index(atom, candidate); idx > 0 {
				field = strings.TrimSpace(atom[:idx])
				op = candidate
				value = atom[idx+len(candidate):]
				break
			}
		}
	}
	if op == "" {
		return false, fmt.Errorf("无法识别的条件 %q", atom)
	}
	value = trimQuotes(value)
	if strings.HasPrefix(value, "~") || strings.HasPrefix(value, "=") || strings.HasPrefix(value, "!") {
		return false, fmt.Errorf("运算符重复：%q", atom)
	}

	actual, ok := resolveExprField(field, ctx)
	if !ok {
		return false, fmt.Errorf("未知字段 %q", field)
	}

	result := false
	switch op {
	case "==":
		result = actual == value
	case "!=":
		result = actual != value
	case "contains":
		result = strings.Contains(actual, value)
	case "~", "!~":
		re, err := regexp.Compile(value)
		if err != nil {
			return false, fmt.Errorf("正则非法 %q: %v", value, err)
		}
		matched := re.MatchString(actual)
		if op == "~" {
			result = matched
		} else {
			result = !matched
		}
	default:
		return false, fmt.Errorf("未知运算符 %q", op)
	}
	if negate {
		result = !result
	}
	return result, nil
}

func resolveExprField(field string, ctx ruleContext) (string, bool) {
	switch strings.ToLower(field) {
	case "model":
		return ctx.Model, true
	case "path":
		return ctx.Path, true
	case "body":
		return ctx.Body, true
	}
	lower := strings.ToLower(field)
	if strings.HasPrefix(lower, "header:") {
		name := strings.TrimSpace(field[len("header:"):])
		v, _ := lookupHeader(ctx.Headers, name)
		return v, true
	}
	if strings.HasPrefix(lower, "header[") && strings.HasSuffix(field, "]") {
		name := trimQuotes(field[len("header[") : len(field)-1])
		v, _ := lookupHeader(ctx.Headers, name)
		return v, true
	}
	return "", false
}
