import { Icon } from "@iconify-icon/solid"
import { createFileRoute } from "@tanstack/solid-router"

export const Route = createFileRoute("/about")({
  component: AboutPage,
})

function AboutPage() {
  return (
    <div class="flex flex-col items-center justify-center min-h-[calc(100vh-4rem)] px-4">
      <Icon icon="lucide:info" class="size-16 text-purple-500 mb-6" />
      <h1 class="text-4xl font-bold text-gray-900 mb-3">关于</h1>
      <p class="text-lg text-gray-600 max-w-md text-center leading-relaxed">
        Suki Chat 是一个基于 SolidJS 构建的聊天应用。
        <br />
        使用 TanStack Router 管理路由，Tailwind CSS 提供样式。
      </p>
    </div>
  )
}
