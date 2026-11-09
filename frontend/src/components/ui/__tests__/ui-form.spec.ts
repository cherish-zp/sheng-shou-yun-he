import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, h, nextTick, ref } from 'vue'
import { Checkbox } from '../checkbox'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../select'
import { Switch } from '../switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '../tabs'

describe('Checkbox', () => {
  it('点击触发 update:modelValue', async () => {
    const onUpdate = vi.fn()
    const wrapper = mount(Checkbox, {
      props: { modelValue: false, 'onUpdate:modelValue': onUpdate },
    })
    // 组件 vnode.el 指向 fragment 锚点,须点击实际渲染的 button 节点。
    await wrapper.find('button').trigger('click')
    expect(onUpdate).toHaveBeenCalledWith(true)
    expect(wrapper.emitted('update:modelValue')).toHaveLength(1)
  })

  it('v-model 双向:勾选后 data-state 变为 checked,再点取消', async () => {
    const state = ref<boolean | 'indeterminate'>(false)
    const wrapper = mount(defineComponent({
      setup: () => () => h(Checkbox, {
        modelValue: state.value,
        'onUpdate:modelValue': (v: boolean | 'indeterminate') => { state.value = v },
        'data-test': 'cb',
      }),
    }))
    const cb = () => wrapper.find('[data-test="cb"]')
    await cb().trigger('click')
    await nextTick()
    expect(state.value).toBe(true)
    expect(cb().attributes('data-state')).toBe('checked')

    await cb().trigger('click')
    await nextTick()
    expect(state.value).toBe(false)
    expect(cb().attributes('data-state')).toBe('unchecked')
  })
})

describe('Switch', () => {
  it('点击切换 v-model 并反映 data-state', async () => {
    const state = ref(false)
    const wrapper = mount(defineComponent({
      setup: () => () => h(Switch, {
        modelValue: state.value,
        'onUpdate:modelValue': (v: boolean) => { state.value = v },
        'data-test': 'sw',
      }),
    }))
    const sw = () => wrapper.find('[data-test="sw"]')
    expect(sw().attributes('data-state')).toBe('unchecked')
    await sw().trigger('click')
    await nextTick()
    expect(state.value).toBe(true)
    expect(sw().attributes('data-state')).toBe('checked')
  })

  it('disabled 时点击不触发', async () => {
    const onUpdate = vi.fn()
    const wrapper = mount(Switch, {
      props: { modelValue: false, disabled: true, 'onUpdate:modelValue': onUpdate },
    })
    await wrapper.trigger('click')
    expect(onUpdate).not.toHaveBeenCalled()
  })
})

describe('Tabs', () => {
  it('点击 TabsTrigger 切换 v-model 与激活态', async () => {
    const active = ref('a')
    const wrapper = mount(defineComponent({
      setup: () => () => h(Tabs, {
        modelValue: active.value,
        'onUpdate:modelValue': (v: string | number) => { active.value = String(v) },
      }, {
        default: () => [
          h(TabsList, {}, {
            default: () => [
              h(TabsTrigger, { value: 'a', 'data-test': 'tab-a' }, { default: () => '标签A' }),
              h(TabsTrigger, { value: 'b', 'data-test': 'tab-b' }, { default: () => '标签B' }),
            ],
          }),
          h(TabsContent, { value: 'a' }, { default: () => '内容A' }),
          h(TabsContent, { value: 'b' }, { default: () => '内容B' }),
        ],
      }),
    }))
    expect(wrapper.find('[data-test="tab-a"]').attributes('data-state')).toBe('active')

    // reka-ui 的 TabsTrigger 在 mousedown 上激活。
    await wrapper.find('[data-test="tab-b"]').trigger('mousedown')
    await nextTick()
    expect(active.value).toBe('b')
    expect(wrapper.find('[data-test="tab-b"]').attributes('data-state')).toBe('active')
    expect(wrapper.find('[data-test="tab-a"]').attributes('data-state')).toBe('inactive')
  })
})

describe('Select', () => {
  function mountSelect() {
    const model = ref('')
    const open = ref(false)
    const wrapper = mount(defineComponent({
      setup: () => () => h(Select, {
        modelValue: model.value,
        'onUpdate:modelValue': (v: unknown) => { model.value = String(v) },
        open: open.value,
        'onUpdate:open': (v: boolean) => { open.value = v },
      }, {
        default: () => [
          h(SelectTrigger, { 'data-test': 'select-trigger' }, {
            default: () => h(SelectValue, { placeholder: '请选择' }),
          }),
          h(SelectContent, {}, {
            default: () => [
              h(SelectItem, { value: 'a', 'data-test': 'select-item-a' }, { default: () => '选项A' }),
              h(SelectItem, { value: 'b', 'data-test': 'select-item-b' }, { default: () => '选项B' }),
            ],
          }),
        ],
      }),
    }))
    return { wrapper, model, open }
  }

  it('点击触发器打开选项列表(浮层渲染到 body)', async () => {
    const { wrapper, open } = mountSelect()
    // reka-ui 的 SelectTrigger 在 pointerdown(鼠标左键)上打开;jsdom 没有
    // PointerEvent,VTU 退化为 Event,需显式补 button/ctrlKey/pointerType。
    await wrapper.find('[data-test="select-trigger"]').trigger('pointerdown', {
      pointerType: 'mouse',
      button: 0,
      ctrlKey: false,
      pointerId: 1,
    })
    expect(open.value).toBe(true)
    await vi.waitFor(() => {
      expect(document.querySelector('[data-test="select-item-a"]')).toBeTruthy()
    })
  })

  it('选择选项后更新 v-model 并自动收起', async () => {
    const { wrapper, model, open } = mountSelect()
    await wrapper.find('[data-test="select-trigger"]').trigger('pointerdown', {
      pointerType: 'mouse',
      button: 0,
      ctrlKey: false,
      pointerId: 1,
    })
    const itemA = await vi.waitFor(() => {
      const el = document.querySelector('[data-test="select-item-a"]')
      expect(el).toBeTruthy()
      return el!
    })
    // reka-ui 的 SelectItem 在 pointerup 上完成选中。
    itemA.dispatchEvent(new Event('pointerup', { bubbles: true }))
    await vi.waitFor(() => expect(model.value).toBe('a'))
    expect(open.value).toBe(false)
    // 触发器显示已选值。
    await vi.waitFor(() => {
      expect(wrapper.find('[data-test="select-trigger"]').text()).toContain('选项A')
    })
  })
})
