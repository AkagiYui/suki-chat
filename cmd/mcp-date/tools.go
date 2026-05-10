// tools.go — mcp-date 服务器的工具定义。
// 使用 mcp.go 提供的通用框架，注册 get_current_datetime 工具。
package main

import (
	"encoding/json"
	"fmt"
	"time"
)

const (
	serverName    = "mcp-date"
	serverVersion = "1.0.0"
)

// NewDateHandler 创建一个预注册了日期时间工具的 MCP Handler。
func NewDateHandler() *Handler {
	h := NewHandler(serverName, serverVersion)

	h.RegisterTool(Tool{
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
	}, handleGetCurrentDatetime)

	return h
}

// handleGetCurrentDatetime 获取当前日期时间
func handleGetCurrentDatetime(id any, args json.RawMessage) *Response {
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
				content := []ContentItem{{
					Type: "text",
					Text: fmt.Sprintf("无效的时区: %s。请使用 IANA 时区名称，如 Asia/Shanghai、America/New_York。错误: %v", toolArgs.Timezone, err),
				}}
				return newResult(id, CallToolResult{Content: content, IsError: true})
			}
		}
	}

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

	return newResult(id, CallToolResult{
		Content: []ContentItem{{Type: "text", Text: resultText}},
	})
}
