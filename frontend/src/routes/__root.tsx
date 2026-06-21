import { createRootRoute, Outlet } from "@tanstack/solid-router"
import { onMount } from "solid-js"

import { Devtools } from "@/components/Devtools"
import { Navbar } from "@/components/Navbar"
import { api } from "@/lib/api"
import { logout, setCurrentUser, token, user } from "@/lib/auth"

export const Route = createRootRoute({
  component: RootComponent,
})

function RootComponent() {
  // 启动时若已有令牌但未加载用户，拉取 /me 校验并填充（用于管理员判断、配额展示）。
  onMount(() => {
    if (token() && !user()) {
      api.me()
        .then((r) => setCurrentUser(r.user))
        .catch(() => logout())
    }
  })

  return (
    <div class="min-h-screen bg-gray-50">
      <Navbar />
      <Outlet />
      <Devtools />
    </div>
  )
}
