import { Icon } from "@iconify-icon/solid"
import { createFileRoute, useNavigate } from "@tanstack/solid-router"
import { createEffect, createResource, For, Show } from "solid-js"

import { api } from "@/lib/api"
import { token, user } from "@/lib/auth"

export const Route = createFileRoute("/admin")({
  component: AdminPage,
})

function AdminPage() {
  const navigate = useNavigate()
  // 仅管理员可见；其他情况重定向。
  createEffect(() => {
    if (!token()) {
      navigate({ to: "/login" })
    } else if (user() && user()!.role !== "admin") {
      navigate({ to: "/sessions" })
    }
  })

  const [users] = createResource(() => api.adminListUsers().then((r) => r.users))
  const [sessions] = createResource(() => api.adminListSessions().then((r) => r.sessions))

  return (
    <div class="mx-auto max-w-4xl px-4 py-8 sm:px-6">
      <h1 class="mb-6 flex items-center gap-2 text-2xl font-bold text-gray-900">
        <Icon icon="lucide:shield" class="size-6 text-blue-500" />
        管理后台
      </h1>

      <section class="mb-8">
        <h2 class="mb-3 font-semibold text-gray-700">用户（{users()?.length ?? 0}）</h2>
        <div class="overflow-x-auto rounded-xl border border-gray-200 bg-white">
          <table class="w-full text-left text-sm">
            <thead class="border-b border-gray-200 text-gray-500">
              <tr>
                <th class="px-4 py-2 font-medium">邮箱</th>
                <th class="px-4 py-2 font-medium">角色</th>
                <th class="px-4 py-2 font-medium">剩余配额</th>
              </tr>
            </thead>
            <tbody>
              <For each={users()}>
                {(u) => (
                  <tr class="border-b border-gray-100 last:border-0">
                    <td class="px-4 py-2 text-gray-900">{u.email}</td>
                    <td class="px-4 py-2 text-gray-500">{u.role}</td>
                    <td class="px-4 py-2 text-gray-500">{u.quotaTokens.toLocaleString()}</td>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </div>
      </section>

      <section>
        <h2 class="mb-3 font-semibold text-gray-700">全部会话（{sessions()?.length ?? 0}）</h2>
        <div class="overflow-x-auto rounded-xl border border-gray-200 bg-white">
          <table class="w-full text-left text-sm">
            <thead class="border-b border-gray-200 text-gray-500">
              <tr>
                <th class="px-4 py-2 font-medium">标题</th>
                <th class="px-4 py-2 font-medium">模型</th>
                <th class="px-4 py-2 font-medium">状态</th>
              </tr>
            </thead>
            <tbody>
              <Show
                when={sessions() && sessions()!.length > 0}
                fallback={
                  <tr>
                    <td colspan="3" class="px-4 py-6 text-center text-gray-400">暂无会话</td>
                  </tr>
                }
              >
                <For each={sessions()}>
                  {(s) => (
                    <tr class="border-b border-gray-100 last:border-0">
                      <td class="px-4 py-2 text-gray-900">{s.title}</td>
                      <td class="px-4 py-2 text-gray-500">{s.model}</td>
                      <td class="px-4 py-2 text-gray-500">{s.status}</td>
                    </tr>
                  )}
                </For>
              </Show>
            </tbody>
          </table>
        </div>
      </section>
    </div>
  )
}
