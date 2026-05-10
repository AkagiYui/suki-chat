import { Icon } from "@iconify-icon/solid"
import { createFileRoute } from "@tanstack/solid-router"

export const Route = createFileRoute("/")({
  component: HomePage,
})

function HomePage() {
  return (
    <div class="flex flex-col items-center justify-center min-h-[calc(100vh-4rem)] px-4">
      <Icon icon="lucide:message-circle" class="size-16 text-blue-500 mb-6" />
      <h1 class="text-4xl font-bold text-gray-900 mb-3">Suki Chat</h1>
      <p class="text-lg text-gray-600 max-w-md text-center">
        欢迎来到 Suki Chat，一个简洁的聊天应用。
      </p>
    </div>
  )
}
