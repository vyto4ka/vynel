import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// base './' keeps asset URLs relative, so the UI works under the secret path (docs/STEALTH.md §2.2).
//
// Development: run the panel, then
//   VYNEL_PANEL=http://127.0.0.1:2097/<secret path>/ pnpm dev
// and open http://localhost:5173/ — API calls are proxied to the panel.
const panel = process.env.VYNEL_PANEL

export default defineConfig({
  base: './',
  plugins: [react()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    rollupOptions: { output: { manualChunks: { editor: ['codemirror', '@codemirror/state', '@codemirror/view', '@codemirror/lang-yaml', '@codemirror/lang-json', '@codemirror/language'] } } },
  },
  server: panel
    ? {
        proxy: {
          '/api': {
            target: new URL(panel).origin,
            rewrite: (p) => new URL(panel).pathname.replace(/\/$/, '') + p,
          },
        },
      }
    : undefined,
})
