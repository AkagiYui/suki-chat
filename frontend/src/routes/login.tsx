import { createFileRoute, useNavigate } from "@tanstack/solid-router"

import { AuthForm } from "@/components/AuthForm"
import { api } from "@/lib/api"

export const Route = createFileRoute("/login")({
  component: LoginPage,
})

function LoginPage() {
  const navigate = useNavigate()
  return (
    <AuthForm
      mode="login"
      onSubmit={async (email, password) => {
        await api.login(email, password)
        navigate({ to: "/sessions" })
      }}
    />
  )
}
