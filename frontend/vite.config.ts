import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import ui from '@nuxt/ui/vite'

export default defineConfig({
  plugins: [vue(), ui({ router: false, colorMode: false })],
  server: {
    host: '0.0.0.0',
    port: 6002
  },
  preview: {
    host: '0.0.0.0',
    port: 6002
  }
})
