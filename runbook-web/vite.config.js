import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// HMR/dev server cấu hình cho Envoy reverse-proxy HTTPS với domain runbook.aurora.local
// - host 0.0.0.0: lắng mọi interface trong container
// - allowedHosts: cho phép domain ảo
// - hmr.clientPort 443: client kết nối qua Envoy port 443 (HTTPS)
// - hmr.protocol wss: WebSocket secure khớp với HTTPS từ Envoy
export default defineConfig({
  plugins: [react()],
  server: {
    host: '0.0.0.0',
    port: 5173,
    strictPort: true,
    allowedHosts: ['runbook.aurora.local', 'localhost', '127.0.0.1'],
    hmr: {
      host: 'runbook.aurora.local',
      clientPort: 443,
      protocol: 'wss',
    },
  },
})
