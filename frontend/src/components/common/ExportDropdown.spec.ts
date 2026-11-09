import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ExportDropdown from './ExportDropdown.vue'

// 与 MessageBrowser.spec 的消费方式一致:reka 原生 Content 原地渲染,
// trigger('click') 打开菜单、trigger('click') 选中(组件内转 select)。

function mountDropdown(): ReturnType<typeof mount> {
  return mount(ExportDropdown)
}

describe('ExportDropdown', () => {
  it('菜单默认关闭,点击开关后出现 CSV/JSONL/XLSX 三项', async () => {
    const wrapper = mountDropdown()
    expect(wrapper.find('[data-test="export-csv"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="result-export-xlsx"]').exists()).toBe(false)
    await wrapper.find('[data-test="export-toggle"]').trigger('click')
    expect(wrapper.find('[data-test="export-csv"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="export-jsonl"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="result-export-xlsx"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="result-export-xlsx"]').text()).toContain('XLSX')
  })

  it('选中 XLSX 项:emit export xlsx 并收起菜单', async () => {
    const wrapper = mountDropdown()
    await wrapper.find('[data-test="export-toggle"]').trigger('click')
    await wrapper.find('[data-test="result-export-xlsx"]').trigger('click')
    expect(wrapper.emitted('export')).toEqual([['xlsx']])
    // 选中后同 tick 收起(pick 显式置 open=false)。
    expect(wrapper.find('[data-test="result-export-xlsx"]').exists()).toBe(false)
  })

  it('disabled 传递:导出开关禁用,菜单打不开', async () => {
    const wrapper = mount(ExportDropdown, { props: { disabled: true } })
    const toggle = wrapper.find('[data-test="export-toggle"]')
    expect(toggle.attributes('disabled')).toBeDefined()
    await toggle.trigger('click')
    expect(wrapper.find('[data-test="result-export-xlsx"]').exists()).toBe(false)
  })
})
