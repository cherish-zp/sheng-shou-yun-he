import { describe, expect, it } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import TableDdlCard from './TableDdlCard.vue'

const baseProps = {
  pinned: false,
  loading: false,
  error: '',
  ddl: 'CREATE TABLE `users` (\n  `id` bigint NOT NULL\n)',
  tableName: 'users',
  x: 100,
  y: 80,
}

describe('TableDdlCard', () => {
  it('渲染表名标题与 DDL 正文(pre 滚动区)', () => {
    const wrapper = mount(TableDdlCard, { props: baseProps })
    expect(wrapper.find('[data-test="ddl-card"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="ddl-title"]').text()).toBe('users 建表语句')
    expect(wrapper.find('[data-test="ddl-body"]').text()).toContain('CREATE TABLE `users`')
    // hover 预览态(非 pinned):无复制/关闭按钮。
    expect(wrapper.find('[data-test="ddl-copy"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="ddl-close"]').exists()).toBe(false)
  })

  it('pinned 态显示复制与关闭按钮,点击各自 emit', async () => {
    const wrapper = mount(TableDdlCard, { props: { ...baseProps, pinned: true } })
    await wrapper.find('[data-test="ddl-copy"]').trigger('click')
    expect(wrapper.emitted('copy')).toHaveLength(1)
    await wrapper.find('[data-test="ddl-close"]').trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('loading 态展示加载中,不渲染 DDL 正文', () => {
    const wrapper = mount(TableDdlCard, { props: { ...baseProps, loading: true } })
    expect(wrapper.find('[data-test="ddl-loading"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="ddl-body"]').exists()).toBe(false)
  })

  it('error 态原样展示错误文案,不渲染 DDL 正文', () => {
    const wrapper = mount(TableDdlCard, { props: { ...baseProps, error: '表不存在' } })
    expect(wrapper.find('[data-test="ddl-error"]').text()).toBe('表不存在')
    expect(wrapper.find('[data-test="ddl-body"]').exists()).toBe(false)
  })

  it('按 client 坐标定位并做视口钳位(不超出右/下边缘)', async () => {
    const wrapper = mount(TableDdlCard, { props: { ...baseProps, x: 5000, y: 4000 } })
    // 钳位在挂载后按实测尺寸异步完成(先摆放再测量)。
    await flushPromises()
    // jsdom 无布局(实测尺寸为 0),钳位边界 = innerWidth/innerHeight - 8px 边距。
    const el = wrapper.find('[data-test="ddl-card"]').element as HTMLElement
    expect(el.style.left).toBe(`${window.innerWidth - 8}px`)
    expect(el.style.top).toBe(`${window.innerHeight - 8}px`)
    // 常规坐标原样摆放。
    await wrapper.setProps({ x: 100, y: 80 })
    await flushPromises()
    expect(el.style.left).toBe('100px')
    expect(el.style.top).toBe('80px')
  })
})
