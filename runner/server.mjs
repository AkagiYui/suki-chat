// suki-runner —— 每会话容器内的 agent 运行时（基于 pi）。
//
// 设计：控制平面每会话起一个本容器；容器内用 pi 跑 agent 循环。
// - 模型：指向控制平面的计量代理（OpenAI 兼容），runner 令牌当作 apiKey
//   （pi-ai 会以 Authorization: Bearer <apiKey> 发出，正好被代理的 runnerAuth 校验）。
// - 工具：bash（pi 内置，在本容器内执行）、web_fetch（自定义）、screenshot（自定义，驱动本容器内浏览器）。
// - 事件：订阅 pi 事件并上报到控制平面事件接口（→ SSE/回放）。
// - HTTP：监听 :8088，POST /run {message} 触发一轮；进程常驻以保留会话上下文。

import http from "node:http"
import process from "node:process"

import { AuthStorage, createAgentSession, ModelRegistry, SessionManager } from "@earendil-works/pi-coding-agent"
import { streamSimple, Type } from "@earendil-works/pi-ai"

const CONTROL_URL = (process.env.SUKI_CONTROL_URL || "").replace(/\/$/, "")
const RUNNER_TOKEN = process.env.SUKI_RUNNER_TOKEN || ""
const SESSION_ID = process.env.SUKI_SESSION_ID || ""
const MODEL_ID = process.env.SUKI_MODEL || "deepseek-v4-flash"
const BROWSER_CDP = process.env.SUKI_BROWSER_CDP || "" // 预留：浏览器/截图工具

// 上报事件到控制平面
async function emit(type, data) {
  try {
    await fetch(`${CONTROL_URL}/api/internal/sessions/${SESSION_ID}/events`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${RUNNER_TOKEN}` },
      body: JSON.stringify({ type, data }),
    })
  } catch (e) {
    console.error("[emit] failed:", e?.message || e)
  }
}

// 模型：指向控制平面计量代理
const model = {
  id: MODEL_ID,
  name: MODEL_ID,
  api: "openai-completions",
  provider: "suki",
  baseUrl: `${CONTROL_URL}/api/internal/v1`,
  reasoning: false,
  input: ["text"],
  cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
  contextWindow: 64000,
  maxTokens: 8192,
}

// 自定义工具：web_fetch（在本容器内联网抓取并抽取正文）
const webFetchTool = {
  name: "web_fetch",
  label: "Web Fetch",
  description: "抓取给定 URL 的网页内容并返回纯文本。",
  parameters: Type.Object({ url: Type.String({ description: "要抓取的完整 URL（含 http/https）" }) }),
  execute: async (_id, params) => {
    const res = await fetch(params.url, { headers: { "User-Agent": "suki-runner/0.1" } })
    const html = await res.text()
    const text = html
      .replace(/<script[\s\S]*?<\/script>/gi, " ")
      .replace(/<style[\s\S]*?<\/style>/gi, " ")
      .replace(/<[^>]+>/g, " ")
      .replace(/\s+/g, " ")
      .trim()
      .slice(0, 8000)
    return { content: [{ type: "text", text: `[HTTP ${res.status}] ${params.url}\n\n${text}` }], details: {} }
  },
}

let sessionPromise
async function getSession() {
  if (!sessionPromise) {
    sessionPromise = (async () => {
      process.chdir("/workspace") // bash/文件工具在工作区目录操作

      // pi 通过 ModelRegistry+AuthStorage 解析 provider 的 apiKey：
      // 把 runner 令牌登记为 provider "suki" 的运行时密钥，pi 会以 Bearer 发给我们的代理。
      const authStorage = AuthStorage.inMemory()
      authStorage.setRuntimeApiKey("suki", RUNNER_TOKEN)
      const modelRegistry = ModelRegistry.inMemory(authStorage)

      const { session } = await createAgentSession({
        model,
        authStorage,
        modelRegistry,
        customTools: [webFetchTool],
        sessionManager: SessionManager.inMemory(),
      })
      // 直接注入 runner 令牌作为 apiKey：自定义 provider "suki" 未在注册表登记，
      // pi 的默认 auth 解析不会传 key，这里用 streamFn 包装确保每次调用都带上。
      session.agent.streamFn = (m, ctx, opts = {}) => {
        emit("model_call", { model: m?.id })
        return streamSimple(m, ctx, { ...opts, apiKey: opts.apiKey || RUNNER_TOKEN })
      }

      // 订阅 pi 事件 → 映射为我们的事件协议并上报。
      // pi 0.79 在 message_end 携带完整消息：assistant.content 为
      // [{type:"text"|"thinking"|"toolCall"}…]；toolResult 携带工具输出。
      session.subscribe((ev) => {
        if (ev.type !== "message_end") return
        const m = ev.message || {}
        if (m.role === "assistant") {
          if (m.stopReason === "error") {
            emit("error", { message: m.errorMessage || "模型调用出错" })
            return
          }
          for (const block of m.content || []) {
            if (block.type === "text" && block.text?.trim()) {
              emit("assistant_message", { content: block.text })
            } else if (block.type === "toolCall") {
              emit("tool_call", { name: block.name, arguments: JSON.stringify(block.arguments ?? {}) })
            }
          }
        } else if (m.role === "toolResult") {
          const text = (m.content || []).map((c) => c.text || "").join("\n").slice(0, 8000)
          emit("tool_result", { name: m.toolName, output: text, error: !!m.isError })
        }
      })
      return session
    })()
  }
  return sessionPromise
}

async function runOnce(message) {
  await emit("user_message", { content: message })
  const session = await getSession()
  await session.prompt(message)
  await emit("done", { finishReason: "stop" })
}

// --once 模式：跑一条消息后退出，便于调试 pi
if (process.argv[2] === "--once") {
  runOnce(process.argv[3] || "你好")
    .then(() => process.exit(0))
    .catch((e) => {
      console.error(e)
      emit("error", { message: String(e?.message || e) }).finally(() => process.exit(1))
    })
} else {
  const server = http.createServer(async (req, res) => {
    if (req.method === "POST" && req.url === "/run") {
      let body = ""
      for await (const c of req) body += c
      let message = ""
      try {
        message = JSON.parse(body || "{}").message || ""
      } catch {
        res.writeHead(400)
        res.end("bad json")
        return
      }
      try {
        await runOnce(message)
        res.writeHead(200, { "Content-Type": "application/json" })
        res.end(JSON.stringify({ status: "ok" }))
      } catch (e) {
        await emit("error", { message: String(e?.message || e) })
        res.writeHead(500)
        res.end(JSON.stringify({ error: String(e?.message || e) }))
      }
    } else if (req.url === "/health") {
      res.writeHead(200)
      res.end("ok")
    } else {
      res.writeHead(404)
      res.end()
    }
  })
  server.listen(8088, () => console.log(`suki-runner listening :8088 (session ${SESSION_ID}, browser=${BROWSER_CDP || "off"})`))
}
