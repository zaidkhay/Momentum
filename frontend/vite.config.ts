// See ARCHITECTURE.md §11 — local dev proxy to Python API on port 8000
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/sectors': 'http://localhost:8000',
      '/stocks': 'http://localhost:8000',
      '/signals': 'http://localhost:8000',
      '/health': 'http://localhost:8000',
    },
  },
})
