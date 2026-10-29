package main

import (
	"strings"
)

type endpointKind int

const (
	epChat endpointKind = iota
	epResponses
	epMessages
)

func (k endpointKind) String() string {
	switch k {
	case epChat:
		return "chat.completions"
	case epResponses:
		return "responses"
	case epMessages:
		return "messages"
	}
	return "unknown"
}

func (k endpointKind) upstreamPath() string {
	switch k {
	case epChat:
		return "/v1/chat/completions"
	case epResponses:
		return "/v1/responses"
	case epMessages:
		return "/v1/messages"
	}
	return "/v1/chat/completions"
}

type injectResult int

const (
	injectSkipped injectResult = iota
	injectApplied
	injectBadShape
)

func (r injectResult) String() string {
	switch r {
	case injectApplied:
		return "已注入"
	case injectBadShape:
		return "结构冲突"
	}
	return "未注入"
}

func injectStrategy(body map[string]any, kind endpointKind, instruction string) injectResult {
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return injectSkipped
	}
	switch kind {
	case epChat:
		raw, ok := body["messages"]
		if !ok || raw == nil {
			return injectBadShape
		}
		msgs, ok := raw.([]any)
		if !ok {
			return injectBadShape
		}
		head := map[string]any{"role": "system", "content": instruction}
		next := make([]any, 0, len(msgs)+1)
		next = append(next, head)
		next = append(next, msgs...)
		body["messages"] = next
		return injectApplied

	case epResponses:
		raw, exists := body["instructions"]
		if !exists || raw == nil {
			body["instructions"] = instruction
			return injectApplied
		}
		cur, ok := raw.(string)
		if !ok {
			return injectBadShape
		}
		if strings.TrimSpace(cur) == "" {
			body["instructions"] = instruction
		} else {
			body["instructions"] = instruction + "\n\n" + cur
		}
		return injectApplied

	case epMessages:
		raw, exists := body["system"]
		if !exists || raw == nil {
			body["system"] = instruction
			return injectApplied
		}
		switch cur := raw.(type) {
		case string:
			if strings.TrimSpace(cur) == "" {
				body["system"] = instruction
			} else {
				body["system"] = instruction + "\n\n" + cur
			}
			return injectApplied
		case []any:
			head := map[string]any{"type": "text", "text": instruction}
			next := make([]any, 0, len(cur)+1)
			next = append(next, head)
			next = append(next, cur...)
			body["system"] = next
			return injectApplied
		}
		return injectBadShape
	}
	return injectSkipped
}

func matchModelPattern(pattern string, model string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if pattern == model {
		return true
	}
	return strings.Contains(model, pattern)
}

func selectProfile(profiles []Profile, preferredID string, model string) *Profile {
	enabled := make([]Profile, 0, len(profiles))
	for _, p := range profiles {
		if p.Enabled {
			enabled = append(enabled, p)
		}
	}
	if len(enabled) == 0 {
		return nil
	}
	if id := strings.TrimSpace(preferredID); id != "" {
		for i := range enabled {
			if enabled[i].ID == id {
				return &enabled[i]
			}
		}
	}
	for i := range enabled {
		for _, pat := range enabled[i].Models {
			if matchModelPattern(pat, model) {
				return &enabled[i]
			}
		}
	}
	for i := range enabled {
		if len(enabled[i].Models) == 0 {
			return &enabled[i]
		}
	}
	return nil
}

func resolveModel(u *Upstream, requested string) (string, bool) {
	target := requested
	if v, ok := u.Mappings[requested]; ok && strings.TrimSpace(v) != "" {
		target = v
	} else if u.ID != "" && strings.HasPrefix(requested, u.ID+"/") {
		target = strings.TrimPrefix(requested, u.ID+"/")
	}
	if len(u.Models) == 0 {
		return target, true
	}
	for _, m := range u.Models {
		if m == target {
			return target, true
		}
	}
	return "", false
}
