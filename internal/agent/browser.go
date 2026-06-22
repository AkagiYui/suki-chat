package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/akagiyui/suki-chat/internal/model"
	"github.com/chromedp/chromedp"
	"github.com/google/uuid"
)

// ScreenshotSaver 保存一张截图并返回可访问的 URL。由 session 层注入（写工件 + 发事件）。
type ScreenshotSaver func(name string, png []byte) (url string, err error)

// ScreenshotTool 在真实（隐身）浏览器中打开网页并整页截图，截图直接展示给用户。
//
// 浏览器是独立的 CloakBrowser 服务（CDP，默认 :9222）；本工具通过 chromedp 连接它，
// 导航并截图。截图字节直接由 CDP 传回控制平面，存为会话工件后在前端以图片展示。
func ScreenshotTool(cdpHTTP string, save ScreenshotSaver, emit EventSink) Tool {
	return Tool{
		Def: model.ToolDef{
			Type: "function",
			Function: model.FunctionDef{
				Name: "screenshot_page",
				Description: "在真实的隐身浏览器中打开网页并整页截图，截图会直接展示给用户。" +
					"当用户要求“截图 / 看看页面长什么样 / 访问某网站并展示”时使用本工具。",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{"url":{"type":"string","description":"要打开并截图的完整 URL（含 http/https）"}},
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
			png, title, err := captureScreenshot(ctx, cdpHTTP, in.URL)
			if err != nil {
				return "", fmt.Errorf("截图失败（浏览器服务是否已启动？）: %w", err)
			}
			artURL, err := save("shot-"+uuid.NewString()[:8]+".png", png)
			if err != nil {
				return "", fmt.Errorf("保存截图失败: %w", err)
			}
			if emit != nil {
				emit("screenshot", map[string]any{"url": artURL, "pageUrl": in.URL, "title": title})
			}
			return fmt.Sprintf("已在隐身浏览器中打开并整页截图（页面标题：%q），截图已直接展示给用户。", title), nil
		},
	}
}

// captureScreenshot 连接 CDP 浏览器服务，导航到 pageURL 并整页截图。
func captureScreenshot(ctx context.Context, cdpHTTP, pageURL string) (png []byte, title string, err error) {
	wsURL, err := resolveCDPWebSocket(ctx, cdpHTTP)
	if err != nil {
		return nil, "", err
	}

	allocCtx, cancelAlloc := chromedp.NewRemoteAllocator(ctx, wsURL)
	defer cancelAlloc()
	tabCtx, cancelTab := chromedp.NewContext(allocCtx)
	defer cancelTab()
	tabCtx, cancelTimeout := context.WithTimeout(tabCtx, 60*time.Second)
	defer cancelTimeout()

	if err := chromedp.Run(tabCtx,
		chromedp.Navigate(pageURL),
		chromedp.Sleep(2500*time.Millisecond), // 等待页面渲染（含简单 JS）
		chromedp.Title(&title),
		chromedp.FullScreenshot(&png, 80),
	); err != nil {
		return nil, "", err
	}
	return png, title, nil
}

// resolveCDPWebSocket 通过 /json/version 获取浏览器的 WebSocket 调试地址，
// 并把其 host 改写为我们实际连接的 host（容器内部地址可能不可达）。
func resolveCDPWebSocket(ctx context.Context, cdpHTTP string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cdpHTTP, "/")+"/json/version", nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("浏览器服务不可用: %w", err)
	}
	defer resp.Body.Close()

	var v struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", err
	}
	if v.WebSocketDebuggerURL == "" {
		return "", fmt.Errorf("未获取到 CDP webSocketDebuggerUrl")
	}

	base, err := url.Parse(cdpHTTP)
	if err != nil {
		return "", err
	}
	ws, err := url.Parse(v.WebSocketDebuggerURL)
	if err != nil {
		return "", err
	}
	ws.Host = base.Host
	return ws.String(), nil
}
