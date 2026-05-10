import tailwindcss from "@tailwindcss/vite"
import { tanstackRouter } from "@tanstack/router-plugin/vite"
import devtools from "solid-devtools/vite"
import { defineConfig } from "vite"
import solid from "vite-plugin-solid"

export default defineConfig({
  plugins: [
    tailwindcss(),
    tanstackRouter({ target: "solid", autoCodeSplitting: true }),
    devtools({
      autoname: true,
    }),
    solid(),
  ],
  resolve: {
    tsconfigPaths: true, // Vite 8 内置 tsconfig paths 解析，自动读取 tsconfig.json 的 paths 配置
  },
  server: {
    proxy: {
      "/api": {
        target: "http://localhost:8182",
        changeOrigin: true,
      },
    },
  },
})
