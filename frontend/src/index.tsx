/* @refresh reload */
import "./index.css"

import { RouterProvider } from "@tanstack/solid-router"
import { render } from "solid-js/web"

import { getRouter } from "./router"

const router = getRouter()
const rootElement = document.getElementById("root")

if (!rootElement) {
  throw new Error("Root element not found")
}

render(() => <RouterProvider router={router} />, rootElement)
