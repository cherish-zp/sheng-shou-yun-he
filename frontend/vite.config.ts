import {defineConfig} from 'vitest/config'
import vue from '@vitejs/plugin-vue'
import {fileURLToPath, URL} from 'node:url'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    rollupOptions: {
      output: {
        // codemirror 6 体系体积大且随业务代码频繁变动，拆为独立 vendor chunk:
        // 主 chunk 变小、更新时浏览器缓存命中率更高。
        manualChunks(id) {
          if (id.includes('node_modules/@codemirror/') || id.includes('node_modules/@lezer/')) {
            return 'codemirror-vendor'
          }
        },
      },
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    include: ['src/**/*.spec.ts'],
    setupFiles: ['src/test/setup.ts'],
    // jsdom 大组件树(ConnectionTree/KeepAlive 缓存视图)在共享 worker 里
    // 累积内存,会触发 V8 堆耗尽崩溃(worker died, heap out of memory)——
    // 限 4 worker 分摊并发,每个 worker 堆 6GB(4×6=24GB < 32GB 物理内存)。
    minWorkers: 2,
    maxWorkers: 4,
    poolOptions: {
      forks: { execArgv: ['--max-old-space-size=6144'] },
    },
  },
})
