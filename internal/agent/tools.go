package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/akagiyui/suki-chat/internal/model"
	"github.com/akagiyui/suki-chat/internal/sandbox"
)

// Tool 是 agent 可调用的一个工具：模型可见的定义 + 实际执行函数。
type Tool struct {
	Def model.ToolDef
	Run func(ctx context.Context, args json.RawMessage) (string, error)
}

const maxFetchBytes = 512 << 10 // 512KiB
const maxToolOutput = 8000      // 工具输出截断长度（字符）

var (
	reScriptStyle = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	reTags        = regexp.MustCompile(`(?s)<[^>]+>`)
	reWhitespace  = regexp.MustCompile(`[ \t\r\f\v]+`)
	reBlankLines  = regexp.MustCompile(`\n{3,}`)
)

// WebFetchTool 抓取一个网页并返回其纯文本（基础正文抽取）。
func WebFetchTool() Tool {
	httpClient := &http.Client{Timeout: 30 * time.Second}
	return Tool{
		Def: model.ToolDef{
			Type: "function",
			Function: model.FunctionDef{
				Name:        "web_fetch",
				Description: "抓取给定 URL 的网页内容并返回纯文本，用于查阅在线信息。",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{"url":{"type":"string","description":"要抓取的完整 URL（含 http/https）"}},
					"required":["url"]
				}`),
			},
		},
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			if !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://") {
				return "", fmt.Errorf("URL 必须以 http:// 或 https:// 开头")
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.URL, nil)
			if err != nil {
				return "", err
			}
			req.Header.Set("User-Agent", "suki-chat-agent/0.1")
			resp, err := httpClient.Do(req)
			if err != nil {
				return "", fmt.Errorf("抓取失败: %w", err)
			}
			defer resp.Body.Close()
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes))
			text := htmlToText(string(raw))
			return truncate(fmt.Sprintf("[HTTP %d] %s\n\n%s", resp.StatusCode, in.URL, text), maxToolOutput), nil
		},
	}
}

// RunShellTool 在会话沙箱（隔离容器）内执行 shell 命令。
func RunShellTool(sb sandbox.Sandbox) Tool {
	return Tool{
		Def: model.ToolDef{
			Type: "function",
			Function: model.FunctionDef{
				Name:        "run_shell",
				Description: "在你的隔离 Linux 沙箱中执行一条 shell 命令，返回标准输出/错误与退出码。工作目录为 /workspace。",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{"command":{"type":"string","description":"要执行的 shell 命令"}},
					"required":["command"]
				}`),
			},
		},
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Command string `json:"command"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			// 不指定 WorkingDir：让各沙箱用自身默认工作目录
			// （Docker 容器已配置为 /workspace；本地沙箱用其临时工作目录）。
			res, err := sb.Exec(ctx, sandbox.ExecSpec{
				Cmd:        []string{"sh", "-c", in.Command},
				TimeoutSec: 60,
			})
			if err != nil {
				return "", fmt.Errorf("命令执行失败: %w", err)
			}
			var b strings.Builder
			fmt.Fprintf(&b, "exit_code: %d\n", res.ExitCode)
			if res.Stdout != "" {
				fmt.Fprintf(&b, "stdout:\n%s\n", res.Stdout)
			}
			if res.Stderr != "" {
				fmt.Fprintf(&b, "stderr:\n%s\n", res.Stderr)
			}
			return truncate(b.String(), maxToolOutput), nil
		},
	}
}

func htmlToText(html string) string {
	s := reScriptStyle.ReplaceAllString(html, " ")
	s = reTags.ReplaceAllString(s, " ")
	s = reWhitespace.ReplaceAllString(s, " ")
	s = reBlankLines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "\n…（输出已截断）"
}
