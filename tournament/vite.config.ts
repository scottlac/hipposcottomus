import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// Relative base so a production `dist/` build also works when opened from a
// file:// path or served from any sub-directory on the event laptop.
export default defineConfig({
  base: './',
  plugins: [react(), tailwindcss()],
})
