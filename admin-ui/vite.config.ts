import path from "path"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vite"

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    host: '0.0.0.0',
    port: 5173,
    strictPort: true,
    allowedHosts: ['admin.auroracloud.local', 'adminui.aurora.local', 'adminui-dev.aurora.local', 'localhost', '127.0.0.1'],
    hmr: {
      host: 'adminui-dev.aurora.local',
      clientPort: 443,
      protocol: 'wss',
    },
  },

  optimizeDeps: {
    include: ["recharts", "react-is"],
  },
})
