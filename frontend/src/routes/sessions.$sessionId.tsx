import { Icon } from "@iconify-icon/solid"
import { createFileRoute, Link, useNavigate } from "@tanstack/solid-router"
import { createEffect, createResource, createSignal, For, onCleanup, onMount, Show } from "solid-js"

import { api, openEventStream, type SessionEvent } from "@/lib/api"
import { isLoggedIn, token } from "@/lib/auth"

export const Route = createFileRoute("/sessions/$sessionId")({
  component: SessionDetailPage,
})

function str(data: Record<string, unknown>, key: string): string {
  const v = data[key]
  return typeof v === "string" ? v : v === undefined ? "" : String(v)
}

function SessionDetailPage() {
  const navigate = useNavigate()
  const params = Route.useParams()
  createEffect(() => {
    if (!isLoggedIn()) navigate({ to: "/login" })
  })

  const [session, { refetch: refetchSession }] = createResource(
    () => params().sessionId,
    (id) => api.getSession(id).then((r) => r.session),
  )

  const [events, setEvents] = createSignal<SessionEvent[]>([])
  const [running, setRunning] = createSignal(false)
  const [text, setText] = createSignal("")
  let timelineRef: HTMLDivElement | undefined

  onMount(() => {
    const es = openEventStream(params().sessionId, (ev) => {
      setEvents((prev) => [...prev, ev])
      if (ev.type === "done" || ev.type === "error") {
        setRunning(false)
        refetchSession()
      }
      if (ev.type === "user_message") setRunning(true)
    })
    onCleanup(() => es.close())
  })

  // 进入时若会话已在运行，标记为运行中
  createEffect(() => {
    if (session()?.status === "running") setRunning(true)
  })

  // 新事件到达后自动滚动到底部
  createEffect(() => {
    events()
    if (timelineRef) timelineRef.scrollTop = timelineRef.scrollHeight
  })

  async function send(e: Event) {
    e.preventDefault()
    const content = text().trim()
    if (!content || running()) return
    setText("")
    setRunning(true)
    try {
      await api.sendMessage(params().sessionId, content)
    } catch (err) {
      setRunning(false)
      alert(err instanceof Error ? err.message : "发送失败")
    }
  }

  async function hibernate() {
    try {
      await api.hibernate(params().sessionId)
      refetchSession()
    } catch (err) {
      alert(err instanceof Error ? err.message : "操作失败")
    }
  }

  return (
    <div class="mx-auto flex h-[calc(100vh-4rem)] max-w-3xl flex-col px-4 sm:px-6">
      <header class="flex items-center gap-3 border-b border-gray-200 py-3">
        <Link to="/sessions" class="rounded-lg p-2 text-gray-500 hover:bg-gray-100" aria-label="返回">
          <Icon icon="lucide:arrow-left" class="size-5" />
        </Link>
        <div class="min-w-0 flex-1">
          <h1 class="truncate font-semibold text-gray-900">{session()?.title ?? "会话"}</h1>
          <p class="truncate text-xs text-gray-400">{session()?.model}</p>
        </div>
        <Show when={running()}>
          <span class="flex items-center gap-1 text-xs text-blue-600">
            <Icon icon="lucide:loader-circle" class="size-4 animate-spin" />
            运行中
          </span>
        </Show>
        <button onClick={hibernate} class="rounded-lg p-2 text-gray-500 hover:bg-gray-100" aria-label="休眠会话">
          <Icon icon="lucide:moon" class="size-5" />
        </button>
      </header>

      <div ref={timelineRef} class="flex-1 space-y-3 overflow-y-auto py-4">
        <Show
          when={events().length > 0}
          fallback={
            <div class="py-16 text-center text-gray-400">
              <Icon icon="lucide:sparkles" class="mb-2 size-8" />
              <p>给 Agent 下达一个任务吧</p>
              <p class="mt-1 text-xs">它会在隔离容器里自主使用工具完成</p>
            </div>
          }
        >
          <For each={events()}>{(ev) => <EventBlock ev={ev} />}</For>
        </Show>
      </div>

      <form onSubmit={send} class="border-t border-gray-200 py-3">
        <div class="flex items-end gap-2">
          <textarea
            value={text()}
            onInput={(e) => setText(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) send(e)
            }}
            rows={1}
            placeholder="输入任务，Enter 发送…"
            class="max-h-32 flex-1 resize-none rounded-xl border border-gray-300 px-3 py-2.5 outline-none focus:border-blue-500"
          />
          <button
            type="submit"
            disabled={running() || text().trim() === ""}
            class="flex size-11 shrink-0 items-center justify-center rounded-xl bg-blue-500 text-white hover:bg-blue-600 disabled:opacity-50"
            aria-label="发送"
          >
            <Icon icon="lucide:send" class="size-5" />
          </button>
        </div>
      </form>
    </div>
  )
}

// EventBlock 把单条会话事件渲染成对应的时间线卡片。
function EventBlock(props: { ev: SessionEvent }) {
  const ev = props.ev
  const d = ev.data

  switch (ev.type) {
    case "user_message":
      return (
        <div class="flex justify-end">
          <div class="max-w-[85%] rounded-2xl rounded-br-sm bg-blue-500 px-4 py-2 text-white">
            {str(d, "content")}
          </div>
        </div>
      )
    case "assistant_message":
      return (
        <div class="flex gap-2">
          <div class="flex size-7 shrink-0 items-center justify-center rounded-full bg-gray-900">
            <Icon icon="lucide:bot" class="size-4 text-white" />
          </div>
          <div class="max-w-[85%] whitespace-pre-wrap rounded-2xl rounded-tl-sm bg-white px-4 py-2 text-gray-900 ring-1 ring-gray-200">
            {str(d, "content")}
          </div>
        </div>
      )
    case "tool_call":
      return (
        <div class="ml-9 rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 text-sm">
          <div class="flex items-center gap-1.5 font-medium text-gray-700">
            <Icon icon="lucide:wrench" class="size-3.5" />
            调用工具 {str(d, "name")}
          </div>
          <pre class="mt-1 overflow-x-auto whitespace-pre-wrap break-all font-mono text-xs text-gray-500">{str(d, "arguments")}</pre>
        </div>
      )
    case "tool_result": {
      const isErr = d.error === true
      return (
        <details class="ml-9 rounded-lg border border-gray-200 bg-gray-50 text-sm">
          <summary class={`cursor-pointer px-3 py-2 font-medium ${isErr ? "text-red-600" : "text-gray-600"}`}>
            <Icon icon="lucide:terminal" class="mr-1 inline size-3.5" />
            工具结果 {str(d, "name")}
          </summary>
          <pre class="overflow-x-auto whitespace-pre-wrap break-all border-t border-gray-200 px-3 py-2 font-mono text-xs text-gray-600">{str(d, "output")}</pre>
        </details>
      )
    }
    case "screenshot": {
      const src = `${str(d, "url")}?token=${encodeURIComponent(token() ?? "")}`
      return (
        <div class="ml-9 overflow-hidden rounded-xl border border-gray-200 bg-white">
          <div class="flex items-center gap-1.5 border-b border-gray-100 px-3 py-2 text-sm text-gray-600">
            <Icon icon="lucide:camera" class="size-3.5" />
            <span class="truncate">网页截图 · {str(d, "title") || str(d, "pageUrl")}</span>
          </div>
          <a href={str(d, "pageUrl")} target="_blank" rel="noreferrer">
            <img src={src} alt="网页截图" class="block w-full" loading="lazy" />
          </a>
        </div>
      )
    }
    case "model_call":
      return (
        <p class="ml-9 text-xs text-gray-400">· 模型思考中（第 {str(d, "iteration")} 步）</p>
      )
    case "usage":
      return (
        <p class="ml-9 text-xs text-gray-300">· 用量 {str(d, "total_tokens")} tokens</p>
      )
    case "error":
      return (
        <div class="ml-9 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-600">
          <Icon icon="lucide:circle-alert" class="mr-1 inline size-4" />
          {str(d, "message")}
        </div>
      )
    case "done":
      return (
        <div class="flex items-center justify-center gap-1 py-1 text-xs text-gray-300">
          <Icon icon="lucide:check" class="size-3.5" />
          本轮完成
        </div>
      )
    default:
      return null
  }
}
