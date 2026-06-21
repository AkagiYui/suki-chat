import { createSignal } from "solid-js"

// 全局认证状态：令牌持久化在 localStorage，用户信息在内存。
export interface User {
  id: string
  email: string
  role: "user" | "admin"
  quotaTokens: number
  createdAt: string
}

const TOKEN_KEY = "suki_token"

const [token, setTokenSignal] = createSignal<string | null>(localStorage.getItem(TOKEN_KEY))
const [user, setUser] = createSignal<User | null>(null)

export { token, user }

// setSession 登录/注册成功后保存令牌与用户。
export function setSession(t: string, u: User) {
  setTokenSignal(t)
  setUser(u)
  localStorage.setItem(TOKEN_KEY, t)
}

// setCurrentUser 仅更新用户信息（如拉取 /me 后）。
export function setCurrentUser(u: User | null) {
  setUser(u)
}

// logout 清除登录态。
export function logout() {
  setTokenSignal(null)
  setUser(null)
  localStorage.removeItem(TOKEN_KEY)
}

export function isLoggedIn() {
  return token() !== null
}
