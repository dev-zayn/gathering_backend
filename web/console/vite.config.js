import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'

// The build lands in dist/, which nginx serves. The console is static files
// on their own origin, calling the API at VITE_API_URL.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { '@': path.resolve(import.meta.dirname, 'src') } },
  base: '/',
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes('node_modules')) return undefined
          if (/node_modules\/(react|react-dom|scheduler|react-router|react-router-dom)\//.test(id)) return 'react'
          return undefined
        },
      },
    },
  },
  server: {
    // Dev only, so nobody fights CORS while editing.
    proxy: { '/v1': 'http://localhost:8080' },
  },
})
