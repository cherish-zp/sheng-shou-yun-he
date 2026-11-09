<script setup lang="ts">
// 客户端首页(无激活 tab 时):展示已有能力。「新建连接」在左上角
// 连接树,此处不再重复提供入口。DBX 化:lucide 图标 + 语义 token。
import type { Component } from 'vue'
import { Command, Database, Globe, KeyRound, Pencil, Rocket, Terminal, Wrench, Zap } from 'lucide-vue-next'

interface Feature {
  icon: Component
  title: string
  desc: string
}

// 支持的数据源(徽标条,与后端 builtinDrivers 保持一致)。
const SOURCES = [
  { icon: '⚡', name: 'Kafka' },
  { icon: '🐬', name: 'MySQL' },
  { icon: '🌿', name: 'TiDB' },
  { icon: '🐘', name: 'PostgreSQL' },
  { icon: '🐝', name: 'Hive' },
  { icon: '🗄️', name: 'ClickHouse' },
  { icon: '🔎', name: 'Elasticsearch' },
  { icon: '🧱', name: 'Redis' },
]

const FEATURES: Feature[] = [
  { icon: Database, title: '八大数据源', desc: '统一连接管理与库表树,连接状态一目了然' },
  { icon: Terminal, title: '六套 SQL 控制台', desc: '多语句执行、结果分页、查询文件保存与快捷键' },
  { icon: Pencil, title: '数据编辑', desc: '主键/整行定位的单元格编辑与行删除,预览确认防误操作' },
  { icon: Wrench, title: '表管理', desc: '右键截断、删表、编辑表字段、导出表结构' },
  { icon: Zap, title: 'Kafka 全家桶', desc: '消息浏览、批量生产、消费组 Lag 总览、集群健康' },
  { icon: KeyRound, title: 'Redis 管理', desc: '五种类型读写、TTL 设置与内存占用查看' },
  { icon: Command, title: '效率工具', desc: '⌘K 命令面板、tab 草稿、深浅色主题、操作审计' },
  { icon: Rocket, title: '自更新与文档', desc: '新版本红点提醒、原位无感升级、内置使用文档' },
]
</script>

<template>
  <div class="flex h-full flex-col items-center justify-start gap-7 overflow-auto p-8 pt-10" data-test="home-view">
    <section class="max-w-[560px] text-center">
      <div class="mx-auto mb-2.5 flex size-12 items-center justify-center rounded-xl border border-border bg-muted text-muted-foreground">
        <Globe class="size-6" />
      </div>
      <h1 class="mb-2.5 text-2xl font-semibold tracking-tight text-foreground" data-test="home-title">多数据源数据库管理客户端</h1>
      <p class="text-sm leading-relaxed text-muted-foreground">一个客户端管理八类数据源：浏览、查询、编辑、导出一站式完成。</p>
      <div class="mt-3.5 flex flex-wrap justify-center gap-2" data-test="home-sources">
        <span
          v-for="src in SOURCES"
          :key="src.name"
          class="inline-flex items-center gap-1.5 rounded-full border border-border bg-card px-2.5 py-0.5 text-xs text-muted-foreground"
          data-test="home-source-badge"
        >
          <span class="text-[13px]">{{ src.icon }}</span>{{ src.name }}
        </span>
      </div>
    </section>

    <section class="grid w-full max-w-[1080px] grid-cols-2 gap-3 md:grid-cols-4">
      <div
        v-for="f in FEATURES"
        :key="f.title"
        class="flex items-start gap-3 rounded-lg border border-border bg-card p-3.5 transition-colors hover:bg-accent/40"
        data-test="feature-card"
      >
        <component :is="f.icon" class="size-5 flex-none text-muted-foreground" />
        <div class="min-w-0">
          <div class="mb-1 text-[13px] font-semibold text-foreground" data-test="feature-title">{{ f.title }}</div>
          <div class="text-xs leading-relaxed text-muted-foreground" data-test="feature-desc">{{ f.desc }}</div>
        </div>
      </div>
    </section>
  </div>
</template>
