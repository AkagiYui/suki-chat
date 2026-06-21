import { Icon } from "@iconify-icon/solid"
import { Link, useNavigate } from "@tanstack/solid-router"
import { createSignal, Show } from "solid-js"

import { logout, token, user } from "@/lib/auth"

export function Navbar() {
  const navigate = useNavigate()
  const [menuOpen, setMenuOpen] = createSignal(false)

  function handleLogout() {
    logout()
    setMenuOpen(false)
    navigate({ to: "/" })
  }

  return (
    <nav class="flex h-16 items-center justify-between border-b border-gray-200 bg-white px-4 sm:px-6">
      <Link to="/" class="flex items-center gap-2" onClick={() => setMenuOpen(false)}>
        <div class="flex size-7 items-center justify-center rounded-lg bg-blue-500">
          <Icon icon="lucide:bot" class="size-4 text-white" />
        </div>
        <span class="font-semibold text-gray-900">Suki Agent</span>
      </Link>

      {/* 桌面端导航 */}
      <div class="hidden items-center gap-2 sm:flex">
        <Show
          when={token()}
          fallback={
            <>
              <Link to="/login" class="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-50">登录</Link>
              <Link to="/register" class="rounded-lg bg-blue-500 px-4 py-2 text-sm font-medium text-white hover:bg-blue-600">注册</Link>
            </>
          }
        >
          <Link to="/sessions" class="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-50">我的会话</Link>
          <Show when={user()?.role === "admin"}>
            <Link to="/admin" class="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-50">管理</Link>
          </Show>
          <span class="hidden text-xs text-gray-400 md:inline">{user()?.email}</span>
          <button onClick={handleLogout} class="rounded-lg px-3 py-2 text-sm font-medium text-gray-600 hover:bg-gray-50" aria-label="退出登录">
            <Icon icon="lucide:log-out" class="size-4" />
          </button>
        </Show>
      </div>

      {/* 移动端汉堡按钮 */}
      <button class="rounded-lg p-2 text-gray-600 hover:bg-gray-50 sm:hidden" onClick={() => setMenuOpen(!menuOpen())} aria-label="菜单">
        <Icon icon={menuOpen() ? "lucide:x" : "lucide:menu"} class="size-5" />
      </button>

      {/* 移动端下拉菜单 */}
      <Show when={menuOpen()}>
        <div class="absolute inset-x-0 top-16 z-10 border-b border-gray-200 bg-white p-4 shadow-sm sm:hidden">
          <div class="flex flex-col gap-1">
            <Show
              when={token()}
              fallback={
                <>
                  <Link to="/login" class="rounded-lg px-4 py-3 font-medium text-gray-700 hover:bg-gray-50" onClick={() => setMenuOpen(false)}>登录</Link>
                  <Link to="/register" class="rounded-lg px-4 py-3 font-medium text-gray-700 hover:bg-gray-50" onClick={() => setMenuOpen(false)}>注册</Link>
                </>
              }
            >
              <Link to="/sessions" class="rounded-lg px-4 py-3 font-medium text-gray-700 hover:bg-gray-50" onClick={() => setMenuOpen(false)}>我的会话</Link>
              <Show when={user()?.role === "admin"}>
                <Link to="/admin" class="rounded-lg px-4 py-3 font-medium text-gray-700 hover:bg-gray-50" onClick={() => setMenuOpen(false)}>管理</Link>
              </Show>
              <button onClick={handleLogout} class="rounded-lg px-4 py-3 text-left font-medium text-gray-700 hover:bg-gray-50">退出登录</button>
            </Show>
          </div>
        </div>
      </Show>
    </nav>
  )
}
