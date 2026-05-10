// mcp.go — 通用 MCP (Model Context Protocol) 框架。
// 提供 JSON-RPC 2.0 类型定义、Handler 基类、三种传输方式（stdio/SSE/Streamable HTTP）、
// SSE 会话管理和认证中间件。可直接复用于其他 MCP 服务器。
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// JSON-RPC 2.0 协议常量
const (
	jsonrpcVersion     = "2.0"
	mcpProtocolVersion = "2024-11-05"
)

// ============================================================================
// JSON-RPC 类型定义
// ============================================================================

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

// ============================================================================
// MCP 协议相关类型
// ============================================================================

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

// ============================================================================
// ToolHandlerFunc 是工具调用的处理函数类型
// ============================================================================

// ToolHandlerFunc 接收请求 id 和工具参数，返回 JSON-RPC 响应。
// 各 MCP 服务器通过 RegisterTool 注册此类型的处理函数。
type ToolHandlerFunc func(id any, args json.RawMessage) *Response

// ============================================================================
// Handler - 核心 MCP 请求处理器（传输无关）
// ============================================================================

// Handler 处理 MCP JSON-RPC 请求，返回响应。
// 与具体传输方式解耦，stdio / SSE / Streamable HTTP 共用同一套逻辑。
// 通过 RegisterTool 注册工具处理函数。
type Handler struct {
	serverName    string
	serverVersion string
	tools         []Tool
	toolHandlers  map[string]ToolHandlerFunc
}

// NewHandler 创建一个新的 MCP Handler。
func NewHandler(name, version string) *Handler {
	return &Handler{
		serverName:    name,
		serverVersion: version,
		toolHandlers:  make(map[string]ToolHandlerFunc),
	}
}

// RegisterTool 注册一个工具及其处理函数。
func (h *Handler) RegisterTool(tool Tool, fn ToolHandlerFunc) {
	h.tools = append(h.tools, tool)
	h.toolHandlers[tool.Name] = fn
}

// HandleRequest 处理请求并返回 JSON-RPC 响应。
// 对于通知（无 id），返回 nil。
func (h *Handler) HandleRequest(req *Request) *Response {
	switch req.Method {
	case "initialize":
		return h.handleInitialize(req)
	case "tools/list":
		return h.handleToolsList(req)
	case "tools/call":
		return h.handleToolsCall(req)
	case "notifications/initialized":
		// 通知，无需响应
		return nil
	default:
		return newError(req.ID, -32601, "Method not found: "+req.Method)
	}
}

func (h *Handler) handleInitialize(req *Request) *Response {
	return newResult(req.ID, InitializeResult{
		ProtocolVersion: mcpProtocolVersion,
		Capabilities: ServerCapabilities{
			Tools: &ToolsCapability{},
		},
		ServerInfo: ServerInfo{
			Name:    h.serverName,
			Version: h.serverVersion,
		},
	})
}

func (h *Handler) handleToolsList(req *Request) *Response {
	return newResult(req.ID, ListToolsResult{Tools: h.tools})
}

func (h *Handler) handleToolsCall(req *Request) *Response {
	var params CallToolParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return newError(req.ID, -32602, "Invalid params: "+err.Error())
	}

	fn, ok := h.toolHandlers[params.Name]
	if !ok {
		return newError(req.ID, -32602, "Unknown tool: "+params.Name)
	}
	return fn(req.ID, params.Arguments)
}

// ============================================================================
// JSON-RPC 响应构造函数
// ============================================================================

func newResult(id any, result any) *Response {
	return &Response{
		JSONRPC: jsonrpcVersion,
		ID:      id,
		Result:  result,
	}
}

func newError(id any, code int, message string) *Response {
	return &Response{
		JSONRPC: jsonrpcVersion,
		ID:      id,
		Error:   &Error{Code: code, Message: message},
	}
}

// ============================================================================
// stdio 传输
// ============================================================================

// runStdio 以 stdio 模式运行 MCP 服务器（默认传输方式）。
func runStdio(handler *Handler) {
	scanner := bufio.NewScanner(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)

	// 足够大的缓冲区（MCP 客户端可能发送较大的消息）
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			writeStdioResponse(writer, newError(nil, -32700, "Parse error: "+err.Error()))
			continue
		}

		resp := handler.HandleRequest(&req)
		if resp != nil {
			writeStdioResponse(writer, resp)
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "读取标准输入出错: %v\n", err)
		os.Exit(1)
	}
}

func writeStdioResponse(writer *bufio.Writer, resp *Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "序列化响应出错: %v\n", err)
		return
	}
	writer.Write(data)
	writer.WriteByte('\n')
	writer.Flush()
}

// ============================================================================
// HTTP 传输（SSE + Streamable HTTP）
// ============================================================================

// runHTTP 以 HTTP 模式运行 MCP 服务器，支持 SSE 和 Streamable HTTP。
// mode 参数为 "sse" 或 "streamable"。
func runHTTP(handler *Handler, host, port, authToken, mode string) {
	sseManager := newSSEManager()
	mux := http.NewServeMux()

	// 公共路由
	mux.HandleFunc("/health", handleHealth)

	switch mode {
	case "sse":
		// SSE 传输: GET /sse 建立连接, POST /message 发送请求
		mux.HandleFunc("/sse", makeSSEHandler(sseManager))
		mux.HandleFunc("/message", makeMessageHandler(handler, sseManager))
		log.Printf("[SSE] 服务器启动在 %s:%s", host, port)
	case "streamable":
		// Streamable HTTP: POST /mcp 处理请求
		mux.HandleFunc("/mcp", makeStreamableHandler(handler))
		log.Printf("[Streamable HTTP] 服务器启动在 %s:%s", host, port)
	}

	// 包装认证中间件
	var server http.Handler = mux
	if authToken != "" {
		server = authMiddleware(authToken)(mux)
		log.Printf("已启用 Bearer Token 认证")
	}

	addr := fmt.Sprintf("%s:%s", host, port)
	srv := &http.Server{Addr: addr, Handler: server}

	// 优雅退出
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("正在关闭服务器...")
		srv.Close()
	}()

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("HTTP 服务器错误: %v", err)
	}
	log.Println("服务器已关闭")
}

// ============================================================================
// SSE 会话管理
// ============================================================================

// SSESession 代表一个 SSE 客户端连接
type SSESession struct {
	ID       string
	Messages chan []byte         // 用于向 SSE 连接推送消息
	Done     chan struct{}       // 连接关闭信号
	writer   http.ResponseWriter // SSE 响应写入器
	flusher  http.Flusher
	mu       sync.Mutex
}

// SSEManager 管理所有 SSE 会话
type SSEManager struct {
	mu       sync.RWMutex
	sessions map[string]*SSESession
}

func newSSEManager() *SSEManager {
	return &SSEManager{
		sessions: make(map[string]*SSESession),
	}
}

func (m *SSEManager) add(session *SSESession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.ID] = session
}

func (m *SSEManager) remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
}

func (m *SSEManager) get(id string) *SSESession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[id]
}

// generateSessionID 生成随机会话 ID
func generateSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ============================================================================
// SSE 传输处理器
// ============================================================================

// makeSSEHandler 创建 SSE 连接处理器
// GET /sse — 客户端连接后，服务器返回 endpoint 事件告知消息端点
func makeSSEHandler(manager *SSEManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "不支持 SSE", http.StatusInternalServerError)
			return
		}

		// 设置 SSE 响应头
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no") // 禁用 nginx 缓冲
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		session := &SSESession{
			ID:       generateSessionID(),
			Messages: make(chan []byte, 64),
			Done:     make(chan struct{}),
			writer:   w,
			flusher:  flusher,
		}
		manager.add(session)
		defer manager.remove(session.ID)

		// 发送 endpoint 事件，告知客户端消息端点
		endpointURL := fmt.Sprintf("/message?sessionId=%s", session.ID)
		fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", endpointURL)
		flusher.Flush()

		log.Printf("[SSE] 新会话: %s", session.ID)

		// 阻塞读取消息通道，推送给客户端
		for {
			select {
			case msg := <-session.Messages:
				fmt.Fprintf(w, "event: message\ndata: %s\n\n", msg)
				flusher.Flush()
			case <-r.Context().Done():
				close(session.Done)
				log.Printf("[SSE] 会话断开: %s", session.ID)
				return
			}
		}
	}
}

// makeMessageHandler 创建 SSE 消息处理器
// POST /message?sessionId=xxx — 客户端发送 JSON-RPC 请求，响应通过 SSE 推送
func makeMessageHandler(handler *Handler, manager *SSEManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		sessionID := r.URL.Query().Get("sessionId")
		if sessionID == "" {
			http.Error(w, "缺少 sessionId 参数", http.StatusBadRequest)
			return
		}

		session := manager.get(sessionID)
		if session == nil {
			http.Error(w, "会话不存在或已过期", http.StatusNotFound)
			return
		}

		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			resp := newError(nil, -32700, "Parse error: "+err.Error())
			data, _ := json.Marshal(resp)
			session.Messages <- data
			w.WriteHeader(http.StatusAccepted)
			return
		}

		resp := handler.HandleRequest(&req)
		if resp != nil {
			data, _ := json.Marshal(resp)
			select {
			case session.Messages <- data:
			default:
				log.Printf("[SSE] 会话 %s 消息通道已满", sessionID)
			}
		}

		w.WriteHeader(http.StatusAccepted)
	}
}

// ============================================================================
// Streamable HTTP 传输处理器
// ============================================================================

// makeStreamableHandler 创建 Streamable HTTP 处理器
// POST /mcp — 接收 JSON-RPC 请求，根据 Accept 头决定返回 JSON 还是 SSE 流。
//
// 客户端通过 Accept 头控制响应模式：
//   - Accept: application/json（默认） → 纯一次请求-响应，返回 JSON
//   - Accept: text/event-stream       → SSE 流式返回
func makeStreamableHandler(handler *Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			// 会话终止
			w.WriteHeader(http.StatusOK)
			return
		}

		if r.Method == http.MethodGet {
			// GET 返回 SSE（可选实现，用于服务端主动推送）
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeHTTPJSON(w, http.StatusOK, newError(nil, -32700, "Parse error: "+err.Error()))
			return
		}

		resp := handler.HandleRequest(&req)

		// 通知（无 id）始终返回 202 Accepted
		if resp == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}

		// 根据 Accept 头选择响应模式
		accept := r.Header.Get("Accept")
		if accept == "text/event-stream" {
			// SSE 流式响应
			writeSSEResponse(w, resp)
		} else {
			// 默认：纯 JSON 一次响应
			writeHTTPJSON(w, http.StatusOK, resp)
		}
	}
}

// ============================================================================
// HTTP 辅助函数
// ============================================================================

// handleHealth 健康检查接口
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// writeHTTPJSON 将对象序列化为 JSON 写入 HTTP 响应
func writeHTTPJSON(w http.ResponseWriter, statusCode int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("写入 JSON 响应出错: %v", err)
	}
}

// writeSSEResponse 将 JSON-RPC 响应作为 SSE 事件写入
func writeSSEResponse(w http.ResponseWriter, resp *Response) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		// 降级为普通 JSON
		writeHTTPJSON(w, http.StatusOK, resp)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	data, err := json.Marshal(resp)
	if err != nil {
		log.Printf("SSE 序列化响应出错: %v", err)
		return
	}
	fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
	flusher.Flush()
}

// ============================================================================
// 认证中间件
// ============================================================================

// authMiddleware 检查 Authorization: Bearer <token> 请求头
func authMiddleware(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 健康检查不需要认证
			if r.URL.Path == "/health" {
				next.ServeHTTP(w, r)
				return
			}

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				writeHTTPJSON(w, http.StatusUnauthorized, map[string]string{
					"error": "缺少 Authorization 请求头",
				})
				return
			}

			// 期望格式: Bearer <token>
			const prefix = "Bearer "
			if len(authHeader) < len(prefix) || authHeader[:len(prefix)] != prefix {
				writeHTTPJSON(w, http.StatusUnauthorized, map[string]string{
					"error": "Authorization 格式错误，需要 Bearer <token>",
				})
				return
			}

			providedToken := authHeader[len(prefix):]
			if providedToken != token {
				writeHTTPJSON(w, http.StatusUnauthorized, map[string]string{
					"error": "Token 无效",
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
