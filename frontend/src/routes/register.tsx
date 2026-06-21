import { createFileRoute, useNavigate } from "@tanstack/solid-router"

import { AuthForm } from "@/components/AuthForm"
import { api } from "@/lib/api"

export const Route = createFileRoute("/register")({
  component: RegisterPage,
})

function RegisterPage() {
  const navigate = useNavigate()
  return (
    <AuthForm
      mode="register"
      onSubmit={async (email, password) => {
        await api.register(email, password)
        navigate({ to: "/sessions" })
      }}
    />
  )
}
