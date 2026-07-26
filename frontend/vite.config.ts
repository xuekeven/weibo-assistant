import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  build: {
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes('node_modules/ant-design-vue') || id.includes('node_modules/@ant-design/icons-vue')) return 'ant-design'
          if (id.includes('node_modules/vue')) return 'vue'
          if (id.includes('node_modules/dayjs')) return 'dayjs'
        }
      }
    }
  },
  server: {
    host: '0.0.0.0',
    port: 6002
  },
  preview: {
    host: '0.0.0.0',
    port: 6002
  }
})
