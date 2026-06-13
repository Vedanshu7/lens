import { defineConfig, loadEnv } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const port = parseInt(env.VITE_PORT ?? '5173')
  const target = `http://localhost:${env.VITE_SIDECAR_PORT ?? 8901}`

  return {
    plugins: [react()],
    server: {
      port,
      proxy: {
        '/api':     { target, changeOrigin: true },
        '/metrics': { target, changeOrigin: true },
      },
    },
  }
})
