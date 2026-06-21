// Package model 是上游模型网关：统一的聊天补全接口 + DeepSeek（OpenAI 兼容）实现。
//
// 控制平面所有 LLM 调用都经此网关，便于统一计量、限额与日后多上游路由。
// 会话容器绝不直连上游。
package model

import (
	"context"
	"encoding/json"
)

// 角色常量（OpenAI 兼容）。
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message 是一条对话消息。
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"` // role=tool 时回填对应调用 ID
	Name       string     `json:"name,omitempty"`
}

// ToolCall 是模型发起的一次工具调用。
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // 固定为 "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall 描述被调用的函数及其参数（参数为 JSON 字符串）。
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDef 是提供给模型的工具定义。
type ToolDef struct {
	Type     string      `json:"type"` // "function"
	Function FunctionDef `json:"function"`
}

// FunctionDef 是函数定义，Parameters 为 JSON Schema。
type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Usage 是一次调用的 token 用量，用于计量与配额扣减。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatRequest 是一次聊天补全请求。
type ChatRequest struct {
	Model       string
	Messages    []Message
	Tools       []ToolDef
	Temperature float64
}

// ChatResponse 是一次聊天补全响应。
type ChatResponse struct {
	Message      Message
	FinishReason string
	Usage        Usage
}

// Client 是统一的模型客户端接口。
type Client interface {
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}
