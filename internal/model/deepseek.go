package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DeepSeekClient 是 DeepSeek（OpenAI 兼容）聊天补全客户端。
// 文档：https://api-docs.deepseek.com/zh-cn/api/create-chat-completion
type DeepSeekClient struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// NewDeepSeekClient 创建客户端。baseURL 形如 https://api.deepseek.com。
func NewDeepSeekClient(apiKey, baseURL string) *DeepSeekClient {
	return &DeepSeekClient{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 120 * time.Second},
	}
}

// 以下结构对应 OpenAI 兼容的请求/响应线格式。
type chatRequestWire struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []ToolDef `json:"tools,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	Stream      bool      `json:"stream"`
}

type chatResponseWire struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Chat 发起一次（非流式）聊天补全。
func (c *DeepSeekClient) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("model: 未配置 DeepSeek API Key")
	}
	body, err := json.Marshal(chatRequestWire{
		Model:       req.Model,
		Messages:    req.Messages,
		Tools:       req.Tools,
		Temperature: req.Temperature,
		Stream:      false,
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("model: 请求上游失败: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model: 上游返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var wire chatResponseWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("model: 解析上游响应失败: %w", err)
	}
	if wire.Error != nil {
		return nil, fmt.Errorf("model: 上游错误: %s", wire.Error.Message)
	}
	if len(wire.Choices) == 0 {
		return nil, fmt.Errorf("model: 上游未返回任何 choice")
	}

	return &ChatResponse{
		Message:      wire.Choices[0].Message,
		FinishReason: wire.Choices[0].FinishReason,
		Usage:        wire.Usage,
	}, nil
}
