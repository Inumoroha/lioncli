package prompt

import (
	"maps"
	"strings"
)

// Context 是装配一次 prompt 的运行时输入,对应 Java 的 PromptContext。
//
// 字段可直接用结构体字面量填写;空值即"该段不出现"。装配时统一对各字段做
// trim 规整(见 assembler.normalize),所以这里不强制调用方先清洗。
type Context struct {
	// ApprovalMode 审批策略:auto / never 透传,其余(含空)回落 suggest。
	ApprovalMode string
	// MemoryContext / ExternalContext 拼进动态段「## Project Context」。
	MemoryContext   string
	ExternalContext string
	// SkillIndex 拼进动态段「## Skills」,通常来自 skill.FormatSkillIndex。
	SkillIndex string
	// Variables 用于模式片段里的 {{key}} 替换。
	Variables map[string]string
}

// Variable 取变量值,缺省返回空串(对齐 Java PromptContext.variable)。
func (c Context) Variable(key string) string {
	if c.Variables == nil || key == "" {
		return ""
	}
	return c.Variables[key]
}

// WithVariable 返回一个追加了 (key,value) 的副本,便于链式构造。
// key 为空则原样返回;不修改原 Context 的 map。
func (c Context) WithVariable(key, value string) Context {
	if strings.TrimSpace(key) == "" {
		return c
	}
	vars := make(map[string]string, len(c.Variables)+1)
	maps.Copy(vars, c.Variables)
	vars[strings.TrimSpace(key)] = value
	c.Variables = vars
	return c
}
