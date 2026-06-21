import { Icon } from "@iconify-icon/solid"
import { createFileRoute, Link } from "@tanstack/solid-router"
import { For } from "solid-js"

import { isLoggedIn } from "@/lib/auth"

export const Route = createFileRoute("/")({
  component: HomePage,
})

const features = [
  { icon: "lucide:cloud", title: "云端常驻运行", desc: "关闭页面也不会中断，任务在云端继续，随时回来查看进度。" },
  { icon: "lucide:box", title: "隔离容器", desc: "每个会话独享一个隔离容器，互不干扰，安全运行。" },
  { icon: "lucide:globe", title: "联网与工具", desc: "内置网页抓取、Shell 执行等工具，Agent 自主完成任务。" },
  { icon: "lucide:layers", title: "多会话并行", desc: "同时运行多个会话，各自独立，互不阻塞。" },
]

function HomePage() {
  return (
    <div class="px-4 sm:px-6">
      <section class="mx-auto max-w-3xl py-16 text-center sm:py-24">
        <div class="mb-6 inline-flex size-16 items-center justify-center rounded-2xl bg-blue-500">
          <Icon icon="lucide:bot" class="size-9 text-white" />
        </div>
        <h1 class="mb-4 text-4xl font-bold tracking-tight text-gray-900 sm:text-5xl">
          你的云端 AI Agent
        </h1>
        <p class="mx-auto mb-8 max-w-xl text-lg text-gray-600">
          开箱即用的云端智能体平台。每个会话在隔离容器中运行，关闭页面也不停止，随时回来查看完成情况。
        </p>
        <div class="flex flex-col items-center justify-center gap-3 sm:flex-row">
          <Link
            to={isLoggedIn() ? "/sessions" : "/register"}
            class="inline-flex w-full items-center justify-center gap-2 rounded-lg bg-blue-500 px-6 py-3 font-medium text-white transition-colors hover:bg-blue-600 sm:w-auto"
          >
            <Icon icon="lucide:rocket" class="size-5" />
            {isLoggedIn() ? "进入我的会话" : "免费开始"}
          </Link>
          {!isLoggedIn() && (
            <Link
              to="/login"
              class="inline-flex w-full items-center justify-center gap-2 rounded-lg border border-gray-300 bg-white px-6 py-3 font-medium text-gray-700 transition-colors hover:bg-gray-50 sm:w-auto"
            >
              登录
            </Link>
          )}
        </div>
      </section>

      <section class="mx-auto grid max-w-4xl grid-cols-1 gap-4 pb-20 sm:grid-cols-2">
        <For each={features}>
          {(f) => (
            <div class="rounded-xl border border-gray-200 bg-white p-6">
              <Icon icon={f.icon} class="mb-3 size-7 text-blue-500" />
              <h3 class="mb-1 font-semibold text-gray-900">{f.title}</h3>
              <p class="text-sm text-gray-600">{f.desc}</p>
            </div>
          )}
        </For>
      </section>
    </div>
  )
}
