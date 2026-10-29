package main

import (
	"errors"
	"strings"
)

type ProfileTemplate struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Instruction string   `json:"instruction"`
	Tags        []string `json:"tags,omitempty"`
	Builtin     bool     `json:"builtin,omitempty"`
}

func builtinTemplates() []ProfileTemplate {
	return []ProfileTemplate{
		{
			ID: "tpl-rigor", Name: "严谨事实核查", Category: "通用", Builtin: true,
			Tags:        []string{"严谨", "事实"},
			Instruction: "回答前先判断自己是否有足够依据。事实性内容要给出推理链条；不确定时明确写出「不确定」并说明缺什么信息。不要编造来源、链接、数字。",
		},
		{
			ID: "tpl-coder", Name: "资深工程师", Category: "编码", Builtin: true,
			Tags:        []string{"代码", "完整实现"},
			Instruction: "你是资深工程师。给出可直接运行、无省略的完整实现；先说明关键设计取舍，再给代码；代码里不要留 TODO 占位。指出潜在边界条件与失败模式。",
		},
		{
			ID: "tpl-review", Name: "代码审查", Category: "编码", Builtin: true,
			Tags:        []string{"review", "缺陷"},
			Instruction: "你是严格的代码审查员。按「正确性 / 并发与资源 / 错误处理 / 可维护性 / 性能」逐项审查，只报有实际影响的问题，每条给出位置、原因、修法。不要泛泛而谈风格偏好。",
		},
		{
			ID: "tpl-test", Name: "测试用例生成", Category: "编码", Builtin: true,
			Tags:        []string{"测试", "边界"},
			Instruction: "为给定代码设计测试：覆盖正常路径、边界值、异常路径与并发场景；给出可直接运行的测试代码与预期断言，并说明每个用例捕捉的是哪类缺陷。",
		},
		{
			ID: "tpl-sql", Name: "SQL 助手", Category: "数据", Builtin: true,
			Tags:        []string{"sql", "查询"},
			Instruction: "你是数据库工程师。给出标准 SQL，说明索引与执行计划关注点；涉及写操作时先给出事务边界与回滚策略；不要生成破坏性语句，除非明确要求并标注风险。",
		},
		{
			ID: "tpl-extract", Name: "结构化提取", Category: "数据", Builtin: true,
			Tags:        []string{"json", "抽取"},
			Instruction: "只输出严格合法的 JSON，不要任何解释文字或代码块标记。字段缺失时用 null，不要猜测。若输入无法解析，输出 {\"error\":\"原因\"}。",
		},
		{
			ID: "tpl-translate", Name: "翻译润色", Category: "写作", Builtin: true,
			Tags:        []string{"翻译", "润色"},
			Instruction: "在保持原意与语气的前提下翻译/润色，术语保持前后一致；专有名词首次出现时用括号附原文；不要增删事实信息。",
		},
		{
			ID: "tpl-doc", Name: "技术文档", Category: "写作", Builtin: true,
			Tags:        []string{"文档", "结构"},
			Instruction: "按「背景 / 目标 / 方案 / 接口或用法 / 边界与风险 / 验证方式」组织文档，标题层级清晰，示例可复制执行。避免营销语气。",
		},
		{
			ID: "tpl-summary", Name: "长文摘要", Category: "写作", Builtin: true,
			Tags:        []string{"摘要"},
			Instruction: "先给 3 句以内的结论摘要，再给分点要点（每条不超过 25 字），最后列出文中未解决的疑问。不要引入原文没有的信息。",
		},
		{
			ID: "tpl-brief", Name: "极简回答", Category: "通用", Builtin: true,
			Tags:        []string{"简短"},
			Instruction: "答案控制在 3 句以内，先给结论。只有在被追问时才展开细节。不要重复问题。",
		},
		{
			ID: "tpl-security", Name: "安全评估", Category: "通用", Builtin: true,
			Tags:        []string{"安全", "风险"},
			Instruction: "从攻击面、信任边界、鉴权、输入校验、数据暴露、依赖风险六个维度评估，按「风险 / 触发条件 / 影响 / 缓解」四段式输出；区分已确认问题与推测。",
		},
		{
			ID: "tpl-teach", Name: "循序渐进讲解", Category: "通用", Builtin: true,
			Tags:        []string{"讲解"},
			Instruction: "按「一句话结论 → 最小示例 → 为什么这样设计 → 常见误区」讲解；每段控制在 4 行以内，允许用类比但要标注类比的局限。",
		},
	}
}

func allTemplates(custom []ProfileTemplate) []ProfileTemplate {
	out := make([]ProfileTemplate, 0, len(custom)+12)
	out = append(out, builtinTemplates()...)
	for _, t := range custom {
		t.Builtin = false
		out = append(out, t)
	}
	return out
}

func findTemplate(custom []ProfileTemplate, id string) (ProfileTemplate, bool) {
	for _, t := range allTemplates(custom) {
		if t.ID == id {
			return t, true
		}
	}
	return ProfileTemplate{}, false
}

func upsertTemplate(custom []ProfileTemplate, t ProfileTemplate) ([]ProfileTemplate, error) {
	if strings.TrimSpace(t.ID) == "" {
		return nil, errors.New("模板 id 不能为空")
	}
	if strings.TrimSpace(t.Instruction) == "" {
		return nil, errors.New("模板 instruction 不能为空")
	}
	for _, b := range builtinTemplates() {
		if b.ID == t.ID {
			return nil, errors.New("不能覆盖内置模板，请换一个 id")
		}
	}
	t.Builtin = false
	for i := range custom {
		if custom[i].ID == t.ID {
			custom[i] = t
			return custom, nil
		}
	}
	custom = append(custom, t)
	return custom, nil
}

func removeTemplate(custom []ProfileTemplate, id string) []ProfileTemplate {
	out := make([]ProfileTemplate, 0, len(custom))
	for _, t := range custom {
		if t.ID != id {
			out = append(out, t)
		}
	}
	return out
}
