import { Icon } from "@iconify-icon/solid"
import { Link } from "@tanstack/solid-router"
import { createSignal, Show } from "solid-js"

interface AuthFormProps {
  mode: "login" | "register"
  onSubmit: (email: string, password: string) => Promise<void>
}

// AuthForm 是登录/注册共用的表单卡片，移动端友好。
export function AuthForm(props: AuthFormProps) {
  const [email, setEmail] = createSignal("")
  const [password, setPassword] = createSignal("")
  const [error, setError] = createSignal("")
  const [loading, setLoading] = createSignal(false)

  const isLogin = () => props.mode === "login"

  async function handleSubmit(e: Event) {
    e.preventDefault()
    setError("")
    setLoading(true)
    try {
      await props.onSubmit(email(), password())
    } catch (err) {
      setError(err instanceof Error ? err.message : "操作失败")
    } finally {
      setLoading(false)
    }
  }

  return (
    <div class="flex min-h-[calc(100vh-4rem)] items-center justify-center px-4 py-10">
      <div class="w-full max-w-sm rounded-2xl border border-gray-200 bg-white p-6 sm:p-8">
        <div class="mb-6 text-center">
          <div class="mb-3 inline-flex size-12 items-center justify-center rounded-xl bg-blue-500">
            <Icon icon="lucide:bot" class="size-7 text-white" />
          </div>
          <h1 class="text-xl font-bold text-gray-900">
            {isLogin() ? "登录" : "创建账号"}
          </h1>
        </div>

        <form class="space-y-4" onSubmit={handleSubmit}>
          <div>
            <label class="mb-1 block text-sm font-medium text-gray-700">邮箱</label>
            <input
              type="email"
              required
              value={email()}
              onInput={(e) => setEmail(e.currentTarget.value)}
              class="w-full rounded-lg border border-gray-300 px-3 py-2 text-gray-900 outline-none focus:border-blue-500"
              placeholder="you@example.com"
            />
          </div>
          <div>
            <label class="mb-1 block text-sm font-medium text-gray-700">密码</label>
            <input
              type="password"
              required
              minLength={8}
              value={password()}
              onInput={(e) => setPassword(e.currentTarget.value)}
              class="w-full rounded-lg border border-gray-300 px-3 py-2 text-gray-900 outline-none focus:border-blue-500"
              placeholder="至少 8 位"
            />
          </div>

          <Show when={error()}>
            <p class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-600">{error()}</p>
          </Show>

          <button
            type="submit"
            disabled={loading()}
            class="flex w-full items-center justify-center gap-2 rounded-lg bg-blue-500 px-4 py-2.5 font-medium text-white transition-colors hover:bg-blue-600 disabled:opacity-60"
          >
            <Show when={loading()}>
              <Icon icon="lucide:loader-circle" class="size-4 animate-spin" />
            </Show>
            {isLogin() ? "登录" : "注册"}
          </button>
        </form>

        <p class="mt-5 text-center text-sm text-gray-500">
          {isLogin() ? "还没有账号？" : "已有账号？"}
          <Link
            to={isLogin() ? "/register" : "/login"}
            class="ml-1 font-medium text-blue-600 hover:underline"
          >
            {isLogin() ? "去注册" : "去登录"}
          </Link>
        </p>
      </div>
    </div>
  )
}
