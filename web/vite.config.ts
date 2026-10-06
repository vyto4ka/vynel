import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// base './' keeps asset URLs relative, so the UI works under a secret path (docs/STEALTH.md §2.2).
export default defineConfig({
  base: './',
  plugins: [react()],
  build: { outDir: 'dist', emptyOutDir: true },
})
