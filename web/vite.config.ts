import path from "node:path"
import { defineConfig } from "vite"
import react from "@vitejs/plugin-react"
import tailwindcss from "@tailwindcss/vite"

// The Go server owns port 10020 and proxies UI requests here during development
// (COUCHHUB_DEV_PROXY), so this dev server sits on the next port up. /api is
// proxied straight back so the browser sees a single origin either way.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": path.resolve(import.meta.dirname, "./src") },
  },
  server: {
    // Bind IPv4 explicitly: Vite's default "localhost" resolves to ::1 first on
    // macOS, which the Go dev proxy and process-compose readiness probe - both
    // pointed at 127.0.0.1 - would then fail to reach.
    host: "127.0.0.1",
    port: 10021,
    strictPort: true,
    proxy: {
      "/api": { target: "http://127.0.0.1:10020", changeOrigin: true },
    },
  },
  build: {
    outDir: "dist",
    chunkSizeWarningLimit: 700,
  },
})
