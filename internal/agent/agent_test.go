package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akagiyui/suki-chat/internal/model"
	"github.com/akagiyui/suki-chat/internal/sandbox"
)

// scriptedClient 按预设脚本逐次返回响应，用于确定性测试 agent 循环。
type scriptedClient struct {
	calls     int
	responses []*model.ChatResponse
}

func (c *scriptedClient) Chat(_ context.Context, _ model.ChatRequest) (*model.ChatResponse, error) {
	r := c.responses[c.calls]
	c.calls++
	return r, nil
}

func TestAgentToolLoop(t *testing.T) {
	client := &scriptedClient{responses: []*model.ChatResponse{
		{ // 第一次：发起工具调用
			Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
				{ID: "call_1", Type: "function", Function: model.FunctionCall{Name: "echo", Arguments: `{"text":"hi"}`}},
			}},
			Usage: model.Usage{TotalTokens: 5},
		},
		{ // 第二次：给出最终答复
			Message:      model.Message{Role: model.RoleAssistant, Content: "完成"},
			FinishReason: "stop",
			Usage:        model.Usage{TotalTokens: 7},
		},
	}}

	echo := Tool{
		Def: model.ToolDef{Type: "function", Function: model.FunctionDef{Name: "echo"}},
		Run: func(_ context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(args, &in)
			return "echo:" + in.Text, nil
		},
	}

	var events []string
	a := New(client, "test-model", []Tool{echo}, 8, func(typ string, _ any) {
		events = append(events, typ)
	})

	res, err := a.Run(context.Background(), nil, "你好")
	if err != nil {
		t.Fatalf("Run 失败: %v", err)
	}
	if res.FinalText != "完成" {
		t.Fatalf("最终文本不符: %q", res.FinalText)
	}
	if res.Usage.TotalTokens != 12 {
		t.Fatalf("用量应累计为 12, got %d", res.Usage.TotalTokens)
	}
	if res.Iterations != 2 {
		t.Fatalf("应迭代 2 次, got %d", res.Iterations)
	}
	// 工具结果应作为 tool 消息回填
	var foundToolMsg bool
	for _, m := range res.Messages {
		if m.Role == model.RoleTool && m.Content == "echo:hi" {
			foundToolMsg = true
		}
	}
	if !foundToolMsg {
		t.Fatal("应包含工具结果消息 echo:hi")
	}
	// 关键事件应被发出
	joined := strings.Join(events, ",")
	for _, want := range []string{EvtToolCall, EvtToolResult, EvtDone} {
		if !strings.Contains(joined, want) {
			t.Fatalf("缺少事件 %s，实际: %s", want, joined)
		}
	}
}

func TestWebFetchTool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><head><style>.x{}</style></head><body><h1>标题</h1><p>正文内容</p></body></html>"))
	}))
	defer srv.Close()

	tool := WebFetchTool()
	out, err := tool.Run(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatalf("web_fetch 失败: %v", err)
	}
	if !strings.Contains(out, "标题") || !strings.Contains(out, "正文内容") {
		t.Fatalf("应抽取出正文文本, got: %q", out)
	}
	if strings.Contains(out, "<style>") || strings.Contains(out, ".x{}") {
		t.Fatalf("应去除 style 块, got: %q", out)
	}
}

func TestWebFetchToolRejectsBadURL(t *testing.T) {
	tool := WebFetchTool()
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"url":"ftp://x"}`)); err == nil {
		t.Fatal("非 http(s) URL 应被拒绝")
	}
}

func TestRunShellTool(t *testing.T) {
	p := sandbox.NewLocalProvider()
	sb, _ := p.Create(context.Background(), sandbox.SandboxSpec{SessionID: "shtest"})
	defer sb.Remove(context.Background())

	tool := RunShellTool(sb)
	out, err := tool.Run(context.Background(), json.RawMessage(`{"command":"echo 沙箱里跑"}`))
	if err != nil {
		t.Fatalf("run_shell 失败: %v", err)
	}
	if !strings.Contains(out, "沙箱里跑") || !strings.Contains(out, "exit_code: 0") {
		t.Fatalf("输出不符: %q", out)
	}
}
