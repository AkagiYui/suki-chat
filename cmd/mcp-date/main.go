// mcp-date 是一个 MCP (Model Context Protocol) 服务器，提供获取当前日期时间的能力。
// 支持多种传输方式：stdio、SSE、Streamable HTTP。
// 通过 JSON-RPC 2.0 协议与 MCP 客户端通信。
//
// 运行方式：
//   - stdio（默认）:         go run ./cmd/mcp-date/.
//   - SSE + HTTP:           go run ./cmd/mcp-date/. --transport=sse --port=8080
//   - Streamable HTTP:      go run ./cmd/mcp-date/. --transport=streamable --port=8080
//   - 带认证:                go run ./cmd/mcp-date/. --transport=streamable --auth-token=my-secret
//   - 输出配置 JSON:         go run ./cmd/mcp-date/. --print-config --transport=streamable --port=8080
//
// 项目结构:
//   - main.go  入口、命令行参数、配置输出
//   - mcp.go   通用 MCP 框架（可复用于其他 MCP 服务器）
//   - tools.go 日期时间工具定义
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
)

// ============================================================================
// 命令行参数
// ============================================================================

var (
	transport   = flag.String("transport", "stdio", "传输方式: stdio, sse, streamable")
	port        = flag.String("port", "8080", "HTTP 监听端口")
	host        = flag.String("host", "0.0.0.0", "HTTP 监听地址")
	authToken   = flag.String("auth-token", "", "Bearer Token 认证密钥（为空则不启用认证）")
	printConfig = flag.Bool("print-config", false, "输出 Cherry Studio 可导入的配置 JSON（不启动服务器）")
)

func main() {
	flag.Parse()

	// 如果指定了 --print-config，输出配置 JSON 并退出
	if *printConfig {
		printConfigJSON()
		return
	}

	handler := NewDateHandler()

	switch *transport {
	case "stdio":
		log.SetOutput(os.Stderr) // stdio 模式下日志输出到 stderr
		runStdio(handler)
	case "sse", "streamable":
		runHTTP(handler, *host, *port, *authToken, *transport)
	default:
		log.Fatalf("不支持的传输方式: %s（支持: stdio, sse, streamable）", *transport)
	}
}

// ============================================================================
// 配置输出（用于导入到 Cherry Studio 等 MCP 客户端）
// ============================================================================

// printConfigJSON 根据命令行参数输出 Cherry Studio 兼容的 MCP 配置 JSON。
func printConfigJSON() {
	type mcpServerConfig struct {
		Name        string            `json:"name,omitempty"`
		Type        string            `json:"type"`
		Description string            `json:"description,omitempty"`
		Command     string            `json:"command,omitempty"`
		Args        []string          `json:"args,omitempty"`
		Env         map[string]string `json:"env,omitempty"`
		URL         string            `json:"url,omitempty"`
		BaseURL     string            `json:"baseUrl,omitempty"`
		Headers     map[string]string `json:"headers,omitempty"`
	}

	server := mcpServerConfig{
		Name:        serverName,
		Description: "获取当前日期和时间，支持多时区",
	}

	switch *transport {
	case "stdio":
		server.Type = "stdio"
		server.Command = "go"
		server.Args = []string{"run", "./cmd/mcp-date/."}

	case "sse":
		server.Type = "sse"
		server.URL = fmt.Sprintf("http://%s:%s/sse", *host, *port)
		if *authToken != "" {
			server.Headers = map[string]string{
				"Authorization": "Bearer " + *authToken,
			}
		}

	case "streamable":
		server.Type = "streamableHttp"
		server.URL = fmt.Sprintf("http://%s:%s/mcp", *host, *port)
		if *authToken != "" {
			server.Headers = map[string]string{
				"Authorization": "Bearer " + *authToken,
			}
		}

	default:
		fmt.Fprintf(os.Stderr, "不支持的传输方式: %s\n", *transport)
		os.Exit(1)
	}

	config := map[string]interface{}{
		"mcpServers": map[string]mcpServerConfig{
			serverName: server,
		},
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(config); err != nil {
		fmt.Fprintf(os.Stderr, "序列化配置出错: %v\n", err)
		os.Exit(1)
	}
}
