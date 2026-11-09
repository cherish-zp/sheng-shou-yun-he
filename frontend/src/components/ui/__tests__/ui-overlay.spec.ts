import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, h, ref } from 'vue'
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '../alert-dialog'
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
} from '../context-menu'
import { Dialog, DialogClose, DialogContent, DialogTitle, DialogTrigger } from '../dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '../dropdown-menu'
import { Popover, PopoverContent, PopoverTrigger } from '../popover'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '../tooltip'

/**
 * 浮层组件内容都经 Portal 渲染到 document.body,统一用受控状态
 * (v-model:open)的 harness 驱动;jsdom 不断言浮动定位,只断言
 * 触发器交互、open 状态与 Portal 内容的出现/消失。
 */

describe('Dialog', () => {
  function mountDialog() {
    const open = ref(false)
    const wrapper = mount(defineComponent({
      setup: () => () => h(Dialog, {
        open: open.value,
        'onUpdate:open': (v: boolean) => { open.value = v },
      }, {
        default: () => [
          h(DialogTrigger, { 'data-test': 'dialog-trigger' }, { default: () => '打开' }),
          h(DialogContent, { 'data-test': 'dialog-content' }, {
            default: () => [
              h(DialogTitle, {}, { default: () => '确认操作' }),
              h(DialogClose, { 'data-test': 'dialog-close' }, { default: () => '关闭' }),
            ],
          }),
        ],
      }),
    }))
    return { wrapper, open }
  }

  it('触发器打开,data-test 透传到 Portal 内容', async () => {
    const { wrapper, open } = mountDialog()
    expect(document.querySelector('[data-test="dialog-content"]')).toBeNull()

    await wrapper.find('[data-test="dialog-trigger"]').trigger('click')
    expect(open.value).toBe(true)
    await vi.waitFor(() => {
      const content = document.querySelector('[data-test="dialog-content"]')
      expect(content).toBeTruthy()
      expect(content!.textContent).toContain('确认操作')
    })
  })

  it('DialogClose 关闭后内容卸载', async () => {
    const { wrapper, open } = mountDialog()
    await wrapper.find('[data-test="dialog-trigger"]').trigger('click')
    await vi.waitFor(() => expect(document.querySelector('[data-test="dialog-close"]')).toBeTruthy())

    ;(document.querySelector('[data-test="dialog-close"]') as HTMLElement).click()
    await vi.waitFor(() => {
      expect(open.value).toBe(false)
      expect(document.querySelector('[data-test="dialog-content"]')).toBeNull()
    })
  })
})

describe('AlertDialog', () => {
  it('触发器打开,Cancel 按钮关闭', async () => {
    const open = ref(false)
    const wrapper = mount(defineComponent({
      setup: () => () => h(AlertDialog, {
        open: open.value,
        'onUpdate:open': (v: boolean) => { open.value = v },
      }, {
        default: () => [
          h(AlertDialogTrigger, { 'data-test': 'alert-trigger' }, { default: () => '删除' }),
          h(AlertDialogContent, { 'data-test': 'alert-content' }, {
            default: () => [
              h(AlertDialogTitle, {}, { default: () => '确认删除' }),
              h(AlertDialogCancel, { 'data-test': 'alert-cancel' }, { default: () => '取消' }),
            ],
          }),
        ],
      }),
    }))

    await wrapper.find('[data-test="alert-trigger"]').trigger('click')
    await vi.waitFor(() => {
      const content = document.querySelector('[data-test="alert-content"]')
      expect(content).toBeTruthy()
      expect(content!.textContent).toContain('确认删除')
    })

    ;(document.querySelector('[data-test="alert-cancel"]') as HTMLElement).click()
    await vi.waitFor(() => {
      expect(open.value).toBe(false)
      expect(document.querySelector('[data-test="alert-content"]')).toBeNull()
    })
  })
})

describe('Popover', () => {
  it('触发器切换 open 状态与 Portal 内容', async () => {
    const open = ref(false)
    const wrapper = mount(defineComponent({
      setup: () => () => h(Popover, {
        open: open.value,
        'onUpdate:open': (v: boolean) => { open.value = v },
      }, {
        default: () => [
          h(PopoverTrigger, { 'data-test': 'pop-trigger' }, { default: () => '更多' }),
          h(PopoverContent, { 'data-test': 'pop-content' }, { default: () => '面板内容' }),
        ],
      }),
    }))

    await wrapper.find('[data-test="pop-trigger"]').trigger('click')
    expect(open.value).toBe(true)
    await vi.waitFor(() => {
      const content = document.querySelector('[data-test="pop-content"]')
      expect(content).toBeTruthy()
      expect(content!.textContent).toContain('面板内容')
    })

    await wrapper.find('[data-test="pop-trigger"]').trigger('click')
    await vi.waitFor(() => {
      expect(open.value).toBe(false)
      expect(document.querySelector('[data-test="pop-content"]')).toBeNull()
    })
  })
})

describe('Tooltip', () => {
  it('受控 open 渲染内容,关闭后卸载', async () => {
    const open = ref(false)
    const wrapper = mount(defineComponent({
      setup: () => () => h(TooltipProvider, {}, {
        default: () => h(Tooltip, {
          open: open.value,
          'onUpdate:open': (v: boolean) => { open.value = v },
        }, {
          default: () => [
            h(TooltipTrigger, { 'data-test': 'tip-trigger' }, { default: () => '悬停' }),
            h(TooltipContent, { 'data-test': 'tip-content' }, { default: () => '提示内容' }),
          ],
        }),
      }),
    }))

    expect(document.querySelector('[data-test="tip-content"]')).toBeNull()
    open.value = true
    await vi.waitFor(() => {
      const content = document.querySelector('[data-test="tip-content"]')
      expect(content).toBeTruthy()
      expect(content!.textContent).toContain('提示内容')
    })

    open.value = false
    await vi.waitFor(() => {
      expect(document.querySelector('[data-test="tip-content"]')).toBeNull()
    })
    expect(wrapper.exists()).toBe(true)
  })
})

describe('DropdownMenu', () => {
  it('触发器打开菜单,点击项触发 select 并收起', async () => {
    const onSelect = vi.fn()
    const open = ref(false)
    const wrapper = mount(defineComponent({
      setup: () => () => h(DropdownMenu, {
        open: open.value,
        'onUpdate:open': (v: boolean) => { open.value = v },
      }, {
        default: () => [
          h(DropdownMenuTrigger, { 'data-test': 'menu-trigger' }, { default: () => '菜单' }),
          h(DropdownMenuContent, { 'data-test': 'menu-content' }, {
            default: () => h(DropdownMenuItem, {
              'data-test': 'menu-item',
              onSelect,
            }, { default: () => '动作一' }),
          }),
        ],
      }),
    }))

    await wrapper.find('[data-test="menu-trigger"]').trigger('click')
    expect(open.value).toBe(true)
    const item = await vi.waitFor(() => {
      const el = document.querySelector('[data-test="menu-item"]')
      expect(el).toBeTruthy()
      return el as HTMLElement
    })

    item.click()
    await vi.waitFor(() => expect(onSelect).toHaveBeenCalled())
    await vi.waitFor(() => {
      expect(open.value).toBe(false)
      expect(document.querySelector('[data-test="menu-content"]')).toBeNull()
    })
  })
})

describe('ContextMenu', () => {
  it('右键打开菜单,点击项触发 select 并收起', async () => {
    const onSelect = vi.fn()
    const open = ref(false)
    const wrapper = mount(defineComponent({
      setup: () => () => h(ContextMenu, {
        open: open.value,
        'onUpdate:open': (v: boolean) => { open.value = v },
      }, {
        default: () => [
          h(ContextMenuTrigger, { 'data-test': 'ctx-trigger' }, { default: () => '右键区域' }),
          h(ContextMenuContent, { 'data-test': 'ctx-content' }, {
            default: () => h(ContextMenuItem, {
              'data-test': 'ctx-item',
              onSelect,
            }, { default: () => '复制' }),
          }),
        ],
      }),
    }))

    await wrapper.find('[data-test="ctx-trigger"]').trigger('contextmenu')
    expect(open.value).toBe(true)
    const item = await vi.waitFor(() => {
      const el = document.querySelector('[data-test="ctx-item"]')
      expect(el).toBeTruthy()
      return el as HTMLElement
    })

    item.click()
    await vi.waitFor(() => expect(onSelect).toHaveBeenCalled())
    await vi.waitFor(() => {
      expect(open.value).toBe(false)
      expect(document.querySelector('[data-test="ctx-content"]')).toBeNull()
    })
  })
})
