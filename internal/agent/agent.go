// Package agent 实现 Go 版的 agent 主循环：调用模型网关、执行工具、发出事件。
//
// MVP 说明：v1 的 agent 循环运行在控制平面（goroutine，关页面也不中断），工具中的
// run_shell 在会话的隔离容器内执行。目标架构会把循环本身搬进容器（pi/TS 运行时），
// 届时只需提供一个新的 Runtime 实现，本接口与事件协议保持不变。
package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/akagiyui/suki-chat/internal/model"
)

// 事件类型常量（与前端约定）。
const (
	EvtModelCall  = "model_call"        // 一次模型调用开始
	EvtUsage      = "usage"             // 一次模型调用的 token 用量
	EvtAssistant  = "assistant_message" // 助手文本输出
	EvtToolCall   = "tool_call"         // 模型发起工具调用
	EvtToolResult = "tool_result"       // 工具执行结果
	EvtError      = "error"             // 运行出错
	EvtDone       = "done"              // 本轮结束
)

// EventSink 是事件回调，由 session 层接到事件存储与 SSE 扇出。
type EventSink func(typ string, data any)

// Agent 是一个会话的 agent 运行器。
type Agent struct {
	client   model.Client
	modelID  string
	tools    map[string]Tool
	toolDefs []model.ToolDef
	maxIters int
	emit     EventSink
}

// RunResult 是一次 Run 的结果。
type RunResult struct {
	FinalText  string
	Messages   []model.Message // 含本轮新增（助手/工具）消息的完整历史
	Usage      model.Usage     // 本轮累计用量
	Iterations int
}

// New 创建 Agent。emit 可为 nil（不发事件）。
func New(client model.Client, modelID string, tools []Tool, maxIters int, emit EventSink) *Agent {
	if maxIters <= 0 {
		maxIters = 8
	}
	if emit == nil {
		emit = func(string, any) {}
	}
	a := &Agent{
		client:   client,
		modelID:  modelID,
		tools:    make(map[string]Tool, len(tools)),
		toolDefs: make([]model.ToolDef, 0, len(tools)),
		maxIters: maxIters,
		emit:     emit,
	}
	for _, t := range tools {
		a.tools[t.Def.Function.Name] = t
		a.toolDefs = append(a.toolDefs, t.Def)
	}
	return a
}

// Run 执行 agent 循环：在 history 之后追加 userMsg，循环"模型→工具"直至产出最终答复。
func (a *Agent) Run(ctx context.Context, history []model.Message, userMsg string) (RunResult, error) {
	msgs := append([]model.Message{}, history...)
	msgs = append(msgs, model.Message{Role: model.RoleUser, Content: userMsg})

	var total model.Usage
	for i := 0; i < a.maxIters; i++ {
		a.emit(EvtModelCall, map[string]any{"model": a.modelID, "iteration": i + 1})

		resp, err := a.client.Chat(ctx, model.ChatRequest{
			Model:    a.modelID,
			Messages: msgs,
			Tools:    a.toolDefs,
		})
		if err != nil {
			a.emit(EvtError, map[string]any{"message": err.Error()})
			return RunResult{Messages: msgs, Usage: total, Iterations: i}, err
		}

		total.PromptTokens += resp.Usage.PromptTokens
		total.CompletionTokens += resp.Usage.CompletionTokens
		total.TotalTokens += resp.Usage.TotalTokens
		a.emit(EvtUsage, resp.Usage)

		msgs = append(msgs, resp.Message)
		if resp.Message.Content != "" {
			a.emit(EvtAssistant, map[string]any{"content": resp.Message.Content})
		}

		// 无工具调用 → 本轮结束
		if len(resp.Message.ToolCalls) == 0 {
			a.emit(EvtDone, map[string]any{"finishReason": resp.FinishReason})
			return RunResult{
				FinalText:  resp.Message.Content,
				Messages:   msgs,
				Usage:      total,
				Iterations: i + 1,
			}, nil
		}

		// 执行所有工具调用，结果回填为 tool 消息
		for _, tc := range resp.Message.ToolCalls {
			output := a.runTool(ctx, tc)
			msgs = append(msgs, model.Message{
				Role:       model.RoleTool,
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
				Content:    output,
			})
		}
	}

	a.emit(EvtError, map[string]any{"message": "达到最大迭代次数仍未结束"})
	return RunResult{Messages: msgs, Usage: total, Iterations: a.maxIters}, fmt.Errorf("agent: 达到最大迭代次数")
}

func (a *Agent) runTool(ctx context.Context, tc model.ToolCall) string {
	a.emit(EvtToolCall, map[string]any{
		"id": tc.ID, "name": tc.Function.Name, "arguments": tc.Function.Arguments,
	})

	tool, ok := a.tools[tc.Function.Name]
	if !ok {
		out := fmt.Sprintf("未知工具: %s", tc.Function.Name)
		a.emit(EvtToolResult, map[string]any{"id": tc.ID, "name": tc.Function.Name, "output": out, "error": true})
		return out
	}

	out, err := tool.Run(ctx, json.RawMessage(tc.Function.Arguments))
	if err != nil {
		out = "工具执行错误: " + err.Error()
		a.emit(EvtToolResult, map[string]any{"id": tc.ID, "name": tc.Function.Name, "output": out, "error": true})
		return out
	}
	a.emit(EvtToolResult, map[string]any{"id": tc.ID, "name": tc.Function.Name, "output": out, "error": false})
	return out
}
