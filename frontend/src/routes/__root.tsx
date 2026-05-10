import { createRootRoute, Outlet } from "@tanstack/solid-router"

import { Devtools } from "@/components/Devtools"
import { Navbar } from "@/components/Navbar"

export const Route = createRootRoute({
  component: RootComponent,
})

function RootComponent() {
  return (
    <div class="min-h-screen bg-gray-50">
      <Navbar />
      <Outlet />
      <Devtools />
    </div>
  )
}
