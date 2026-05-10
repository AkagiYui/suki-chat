// mcp-date 是一个 MCP (Model Context Protocol) 服务器，提供获取当前日期时间的能力。
// 通过 stdio 与 MCP 客户端通信，使用 JSON-RPC 2.0 协议。
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// JSON-RPC 2.0 协议常量
const (
	jsonrpcVersion = "2.0"
	serverName     = "mcp-date"
	serverVersion  = "1.0.0"
)

// Request 表示 JSON-RPC 请求
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response 表示 JSON-RPC 响应
type Response struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   *Error `json:"error,omitempty"`
}

// Error 表示 JSON-RPC 错误
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ServerInfo 包含服务器信息
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ServerCapabilities 描述服务器能力
type ServerCapabilities struct {
	Tools *ToolsCapability `json:"tools,omitempty"`
}

// ToolsCapability 表示工具能力
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// InitializeResult 是 initialize 方法的返回结果
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      ServerInfo         `json:"serverInfo"`
}

// Tool 描述一个 MCP 工具
type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"inputSchema"`
}

// InputSchema 描述工具的输入参数 schema
type InputSchema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties,omitempty"`
	Required   []string            `json:"required,omitempty"`
}

// Property 描述单个参数属性
type Property struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// ListToolsResult 是 tools/list 方法的返回结果
type ListToolsResult struct {
	Tools []Tool `json:"tools"`
}

// CallToolParams 是 tools/call 方法的参数
type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// CallToolResult 是 tools/call 方法的返回结果
type CallToolResult struct {
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ContentItem 表示返回内容项
type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)

	// 为 scanner 设置足够大的缓冲区（MCP 客户端可能发送较大的消息）
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			sendError(writer, nil, -32700, "Parse error: "+err.Error())
			continue
		}

		handleRequest(writer, &req)
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "读取标准输入出错: %v\n", err)
		os.Exit(1)
	}
}

// handleRequest 处理单个 JSON-RPC 请求
func handleRequest(writer *bufio.Writer, req *Request) {
	switch req.Method {
	case "initialize":
		handleInitialize(writer, req)
	case "tools/list":
		handleToolsList(writer, req)
	case "tools/call":
		handleToolsCall(writer, req)
	case "notifications/initialized":
		// 客户端初始化完成通知，无需响应
	default:
		sendError(writer, req.ID, -32601, "Method not found: "+req.Method)
	}
}

// handleInitialize 处理 initialize 方法
func handleInitialize(writer *bufio.Writer, req *Request) {
	result := InitializeResult{
		ProtocolVersion: "2024-11-05",
		Capabilities: ServerCapabilities{
			Tools: &ToolsCapability{},
		},
		ServerInfo: ServerInfo{
			Name:    serverName,
			Version: serverVersion,
		},
	}
	sendResult(writer, req.ID, result)
}

// handleToolsList 返回可用工具列表
func handleToolsList(writer *bufio.Writer, req *Request) {
	tools := []Tool{
		{
			Name:        "get_current_datetime",
			Description: "获取当前的日期和时间，支持指定时区。返回包含日期、时间、时区、星期等信息的格式化结果。",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"timezone": {
						Type:        "string",
						Description: "IANA 时区名称，如 Asia/Shanghai、America/New_York、Europe/London 等。不传则使用系统本地时区。",
					},
				},
			},
		},
	}

	sendResult(writer, req.ID, ListToolsResult{Tools: tools})
}

// handleToolsCall 执行工具调用
func handleToolsCall(writer *bufio.Writer, req *Request) {
	var params CallToolParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		sendError(writer, req.ID, -32602, "Invalid params: "+err.Error())
		return
	}

	switch params.Name {
	case "get_current_datetime":
		handleGetCurrentDatetime(writer, req.ID, params.Arguments)
	default:
		sendError(writer, req.ID, -32602, "Unknown tool: "+params.Name)
	}
}

// handleGetCurrentDatetime 获取当前日期时间
func handleGetCurrentDatetime(writer *bufio.Writer, id any, args json.RawMessage) {
	now := time.Now()
	loc := now.Location()

	// 解析参数中的时区设置
	if len(args) > 0 {
		var toolArgs struct {
			Timezone string `json:"timezone"`
		}
		if err := json.Unmarshal(args, &toolArgs); err == nil && toolArgs.Timezone != "" {
			if parsedLoc, err := time.LoadLocation(toolArgs.Timezone); err == nil {
				loc = parsedLoc
				now = now.In(loc)
			} else {
				// 时区无效，返回错误
				content := []ContentItem{
					{
						Type: "text",
						Text: fmt.Sprintf("无效的时区: %s。请使用 IANA 时区名称，如 Asia/Shanghai、America/New_York。错误: %v", toolArgs.Timezone, err),
					},
				}
				sendResult(writer, id, CallToolResult{Content: content, IsError: true})
				return
			}
		}
	}

	// 格式化日期时间信息
	resultText := fmt.Sprintf(`当前日期时间信息:
日期: %s
时间: %s
时区: %s
星期: %s
Unix 时间戳: %d
ISO 8601: %s`,
		now.Format("2006-01-02"),
		now.Format("15:04:05"),
		loc.String(),
		now.Weekday().String(),
		now.Unix(),
		now.Format(time.RFC3339),
	)

	content := []ContentItem{
		{
			Type: "text",
			Text: resultText,
		},
	}

	sendResult(writer, id, CallToolResult{Content: content})
}

// sendResult 发送成功的 JSON-RPC 响应
func sendResult(writer *bufio.Writer, id any, result any) {
	resp := Response{
		JSONRPC: jsonrpcVersion,
		ID:      id,
		Result:  result,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "序列化响应出错: %v\n", err)
		return
	}
	writer.Write(data)
	writer.WriteByte('\n')
	writer.Flush()
}

// sendError 发送 JSON-RPC 错误响应
func sendError(writer *bufio.Writer, id any, code int, message string) {
	resp := Response{
		JSONRPC: jsonrpcVersion,
		ID:      id,
		Error: &Error{
			Code:    code,
			Message: message,
		},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "序列化错误响应出错: %v\n", err)
		return
	}
	writer.Write(data)
	writer.WriteByte('\n')
	writer.Flush()
}
