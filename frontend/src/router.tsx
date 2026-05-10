import { createBrowserHistory } from "@tanstack/history"
import { createRouter as createTanStackRouter } from "@tanstack/solid-router"

import { routeTree } from "./routeTree.gen"

// 使用 HTML5 history 模式路由（浏览器原生 history API）
const history = createBrowserHistory()

export function getRouter() {
  return createTanStackRouter({
    routeTree,
    history,
    scrollRestoration: true,
    defaultPreload: "intent",
    defaultPreloadStaleTime: 0,
  })
}

declare module "@tanstack/solid-router" {
  interface Register {
    router: ReturnType<typeof getRouter>
  }
}
