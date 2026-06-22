import { Icon } from "@iconify-icon/solid"
import { createFileRoute, Link, useNavigate } from "@tanstack/solid-router"
import { createEffect, createResource, createSignal, For, Show } from "solid-js"

import { api } from "@/lib/api"
import { isLoggedIn } from "@/lib/auth"

export const Route = createFileRoute("/sessions/")({
  component: SessionsPage,
})

const statusStyle: Record<string, string> = {
  running: "bg-blue-100 text-blue-700",
  idle: "bg-green-100 text-green-700",
  created: "bg-gray-100 text-gray-600",
  hibernated: "bg-amber-100 text-amber-700",
  stopped: "bg-gray-100 text-gray-600",
  error: "bg-red-100 text-red-700",
}

const statusLabel: Record<string, string> = {
  running: "运行中",
  idle: "空闲",
  created: "已创建",
  hibernated: "已休眠",
  stopped: "已停止",
  error: "出错",
}

function SessionsPage() {
  const navigate = useNavigate()
  createEffect(() => {
    if (!isLoggedIn()) navigate({ to: "/login" })
  })

  const [sessions, { refetch }] = createResource(() => api.listSessions().then((r) => r.sessions))
  const [models] = createResource(() => api.models().then((r) => r.models))
  const [title, setTitle] = createSignal("")
  const [model, setModel] = createSignal("")
  const [indep, setIndep] = createSignal(false)
  const [creating, setCreating] = createSignal(false)

  createEffect(() => {
    const list = models()
    if (list && list.length > 0 && model() === "") setModel(list[0].id)
  })

  async function createSession(e: Event) {
    e.preventDefault()
    setCreating(true)
    try {
      const r = await api.createSession(title() || "新会话", model(), indep())
      navigate({ to: "/sessions/$sessionId", params: { sessionId: r.session.id } })
    } finally {
      setCreating(false)
    }
  }

  async function remove(id: string, e: Event) {
    e.preventDefault()
    e.stopPropagation()
    if (!confirm("确认删除该会话？容器与工作区将一并清除。")) return
    await api.deleteSession(id)
    refetch()
  }

  return (
    <div class="mx-auto max-w-3xl px-4 py-8 sm:px-6">
      <h1 class="mb-6 text-2xl font-bold text-gray-900">我的会话</h1>

      <form onSubmit={createSession} class="mb-8 rounded-xl border border-gray-200 bg-white p-4">
        <div class="flex flex-col gap-3 sm:flex-row">
          <input
            value={title()}
            onInput={(e) => setTitle(e.currentTarget.value)}
            placeholder="会话标题（可选）"
            class="flex-1 rounded-lg border border-gray-300 px-3 py-2 outline-none focus:border-blue-500"
          />
          <select
            value={model()}
            onChange={(e) => setModel(e.currentTarget.value)}
            class="rounded-lg border border-gray-300 px-3 py-2 outline-none focus:border-blue-500"
          >
            <For each={models()}>
              {(m) => <option value={m.id}>{m.label}</option>}
            </For>
          </select>
          <button
            type="submit"
            disabled={creating()}
            class="flex items-center justify-center gap-2 rounded-lg bg-blue-500 px-4 py-2 font-medium text-white hover:bg-blue-600 disabled:opacity-60"
          >
            <Icon icon="lucide:plus" class="size-4" />
            新建
          </button>
        </div>
        <label class="mt-3 flex items-center gap-2 text-sm text-gray-600">
          <input type="checkbox" checked={indep()} onChange={(e) => setIndep(e.currentTarget.checked)} class="size-4" />
          <Icon icon="lucide:globe-lock" class="size-4 text-gray-400" />
          使用独立浏览器（默认与你的其他会话共享一个浏览器）
        </label>
      </form>

      <Show
        when={sessions() && sessions()!.length > 0}
        fallback={
          <div class="rounded-xl border border-dashed border-gray-300 py-16 text-center text-gray-400">
            <Icon icon="lucide:inbox" class="mb-2 size-10" />
            <p>还没有会话，新建一个开始吧</p>
          </div>
        }
      >
        <ul class="space-y-2">
          <For each={sessions()}>
            {(s) => (
              <li class="flex items-center rounded-xl border border-gray-200 bg-white transition-colors hover:border-blue-300">
                <Link
                  to="/sessions/$sessionId"
                  params={{ sessionId: s.id }}
                  class="min-w-0 flex-1 p-4"
                >
                  <div class="flex items-center gap-2">
                    <span class="truncate font-medium text-gray-900">{s.title}</span>
                    <span class={`shrink-0 rounded-full px-2 py-0.5 text-xs ${statusStyle[s.status] ?? "bg-gray-100 text-gray-600"}`}>
                      {statusLabel[s.status] ?? s.status}
                    </span>
                  </div>
                  <p class="mt-0.5 truncate text-xs text-gray-400">{s.model}</p>
                </Link>
                <button
                  onClick={(e) => remove(s.id, e)}
                  class="mr-2 shrink-0 rounded-lg p-2 text-gray-400 hover:bg-red-50 hover:text-red-500"
                  aria-label="删除会话"
                >
                  <Icon icon="lucide:trash-2" class="size-4" />
                </button>
              </li>
            )}
          </For>
        </ul>
      </Show>
    </div>
  )
}
