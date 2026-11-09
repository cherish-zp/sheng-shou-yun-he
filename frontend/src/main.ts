import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
// 字体:Geist Variable(界面)+ Fira Code(等宽),对齐 DBX。
import '@fontsource-variable/geist'
import '@fontsource/fira-code/400.css'
import '@fontsource/fira-code/500.css'
// styles.css(旧手写令牌)已退役:现行为 tokens.css(shadcn/DBX 权威令牌)
// + legacy.css(迁移桥,承载未清偿的 legacy 变量与基类)。
import './styles/globals.css'

// 开发调试:?mock 在浏览器里注入假 API(无 Wails 环境),仅 dev 生效。
if (import.meta.env.DEV && new URLSearchParams(location.search).has('mock')) {
  const { installDevMock } = await import('./test/devMock')
  installDevMock()
}

createApp(App).use(createPinia()).mount('#app')
