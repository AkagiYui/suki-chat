package model

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// 单元测试：用本地 httptest 伪造 OpenAI 兼容响应，校验请求构造与响应解析。
func TestDeepSeekChatUnit(t *testing.T) {
	var gotBody chatRequestWire
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("缺少正确的 Authorization 头: %s", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("路径不符: %s", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"choices":[{"message":{"role":"assistant","content":"你好"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}
		}`)
	}))
	defer srv.Close()

	c := NewDeepSeekClient("test-key", srv.URL)
	resp, err := c.Chat(context.Background(), ChatRequest{
		Model:    "deepseek-v4-flash",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat 失败: %v", err)
	}
	if gotBody.Model != "deepseek-v4-flash" {
		t.Fatalf("请求 model 不符: %s", gotBody.Model)
	}
	if gotBody.Stream {
		t.Fatal("非流式请求 stream 应为 false")
	}
	if resp.Message.Content != "你好" || resp.Usage.TotalTokens != 13 {
		t.Fatalf("响应解析不符: %+v", resp)
	}
}

func TestDeepSeekChatErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid key"}}`)
	}))
	defer srv.Close()
	c := NewDeepSeekClient("bad", srv.URL)
	if _, err := c.Chat(context.Background(), ChatRequest{Model: "m", Messages: []Message{{Role: RoleUser, Content: "x"}}}); err == nil {
		t.Fatal("非 200 应返回错误")
	}
}

func TestDeepSeekNoAPIKey(t *testing.T) {
	c := NewDeepSeekClient("", "https://api.deepseek.com")
	if _, err := c.Chat(context.Background(), ChatRequest{Model: "m"}); err == nil {
		t.Fatal("无 key 应直接报错")
	}
}

// 集成测试：真正调用 DeepSeek，仅在设置了 DEEPSEEK_API_KEY 时运行。
func TestDeepSeekChatIntegration(t *testing.T) {
	key := os.Getenv("DEEPSEEK_API_KEY")
	if key == "" {
		t.Skip("未设置 DEEPSEEK_API_KEY，跳过集成测试")
	}
	c := NewDeepSeekClient(key, "https://api.deepseek.com")
	resp, err := c.Chat(context.Background(), ChatRequest{
		Model: "deepseek-v4-flash",
		Messages: []Message{
			{Role: RoleSystem, Content: "你只回答一个词。"},
			{Role: RoleUser, Content: "用中文说你好"},
		},
	})
	if err != nil {
		t.Fatalf("集成调用失败: %v", err)
	}
	if strings.TrimSpace(resp.Message.Content) == "" {
		t.Fatal("应返回非空内容")
	}
	if resp.Usage.TotalTokens == 0 {
		t.Fatal("应返回 token 用量")
	}
	t.Logf("DeepSeek 回复: %q, 用量: %+v", resp.Message.Content, resp.Usage)
}
