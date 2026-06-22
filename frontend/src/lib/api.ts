import { logout, setSession, token, type User } from "./auth"

// 会话对象（与后端 store.Session 对应）。
export interface Session {
  id: string
  userId: string
  title: string
  model: string
  status: string
  node: string
  createdAt: string
  updatedAt: string
}

export interface ModelInfo {
  id: string
  label: string
}

// 会话事件（SSE 推送）。
export interface SessionEvent {
  seq: number
  type: string
  data: Record<string, unknown>
}

class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  if (body !== undefined) headers["Content-Type"] = "application/json"
  const t = token()
  if (t) headers["Authorization"] = `Bearer ${t}`

  const resp = await fetch(path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  if (resp.status === 401) {
    logout()
  }
  const text = await resp.text()
  const json = text ? JSON.parse(text) : {}
  if (!resp.ok) {
    throw new ApiError(resp.status, json.error || `请求失败 (${resp.status})`)
  }
  return json as T
}

export const api = {
  async register(email: string, password: string) {
    const r = await request<{ token: string, user: User }>("POST", "/api/auth/register", { email, password })
    setSession(r.token, r.user)
    return r
  },
  async login(email: string, password: string) {
    const r = await request<{ token: string, user: User }>("POST", "/api/auth/login", { email, password })
    setSession(r.token, r.user)
    return r
  },
  me() {
    return request<{ user: User }>("GET", "/api/me")
  },
  models() {
    return request<{ models: ModelInfo[] }>("GET", "/api/models")
  },
  listSessions() {
    return request<{ sessions: Session[] }>("GET", "/api/sessions")
  },
  createSession(title: string, model: string, independentBrowser = false) {
    return request<{ session: Session }>("POST", "/api/sessions", { title, model, independentBrowser })
  },
  getSession(id: string) {
    return request<{ session: Session }>("GET", `/api/sessions/${id}`)
  },
  sendMessage(id: string, text: string) {
    return request<{ status: string }>("POST", `/api/sessions/${id}/messages`, { text })
  },
  hibernate(id: string) {
    return request<{ status: string }>("POST", `/api/sessions/${id}/hibernate`)
  },
  deleteSession(id: string) {
    return request<{ status: string }>("DELETE", `/api/sessions/${id}`)
  },
  adminListUsers() {
    return request<{ users: User[] }>("GET", "/api/admin/users")
  },
  adminListSessions() {
    return request<{ sessions: Session[] }>("GET", "/api/admin/sessions")
  },
}

// 已知的事件类型（与后端 agent 事件常量一致）。
const EVENT_TYPES = [
  "user_message",
  "model_call",
  "usage",
  "assistant_message",
  "tool_call",
  "tool_result",
  "screenshot",
  "error",
  "done",
]

// openEventStream 打开会话事件流（SSE）。EventSource 会在断线时自动重连，
// 并通过 Last-Event-ID 续传——配合后端的回放即可"关页面也不丢"。
export function openEventStream(
  sessionId: string,
  onEvent: (ev: SessionEvent) => void,
  lastSeq = 0,
): EventSource {
  const t = token() ?? ""
  const url = `/api/sessions/${sessionId}/events?last_seq=${lastSeq}&token=${encodeURIComponent(t)}`
  const es = new EventSource(url)
  for (const type of EVENT_TYPES) {
    es.addEventListener(type, (e: MessageEvent) => {
      let data: Record<string, unknown> = {}
      try {
        data = JSON.parse(e.data)
      } catch {
        data = { raw: e.data }
      }
      onEvent({ seq: Number(e.lastEventId), type, data })
    })
  }
  return es
}
