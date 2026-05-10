import { Icon } from "@iconify-icon/solid"
import { Link, useRouter } from "@tanstack/solid-router"

export function Navbar() {
  const router = useRouter()

  const links = [
    { href: "/", label: "首页", icon: "lucide:home" },
    { href: "/about", label: "关于", icon: "lucide:info" },
  ]

  function isActive(href: string) {
    return router.state.location.pathname === href
  }

  return (
    <nav class="h-16 border-b border-gray-200 bg-white flex items-center px-6">
      <div class="flex items-center gap-2 mr-8">
        <Icon icon="lucide:message-circle" class="size-6 text-blue-500" />
        <span class="font-semibold text-gray-900">Suki Chat</span>
      </div>
      <div class="flex items-center gap-1">
        {links.map((link) => (
          <Link
            to={link.href}
            class={`
                            flex items-center gap-2 px-4 py-2 rounded-lg text-sm font-medium transition-colors
                            ${isActive(link.href)
            ? "bg-blue-50 text-blue-600"
            : "text-gray-600 hover:text-gray-900 hover:bg-gray-50"
          }
                        `}
          >
            <Icon icon={link.icon} class="size-4" />
            {link.label}
          </Link>
        ))}
      </div>
    </nav>
  )
}
