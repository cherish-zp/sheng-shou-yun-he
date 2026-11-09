import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { h } from 'vue'
import { Alert, AlertDescription, AlertTitle } from '../alert'
import { Badge } from '../badge'
import { Button } from '../button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '../card'
import { Input } from '../input'
import { Label } from '../label'
import { Separator } from '../separator'
import { Skeleton } from '../skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../table'
import { Textarea } from '../textarea'

describe('Button', () => {
  it('应用 variant 与 size 变体类', () => {
    const wrapper = mount(Button, { props: { variant: 'destructive', size: 'lg' } })
    const cls = wrapper.attributes('class') ?? ''
    expect(cls).toContain('bg-destructive')
    expect(cls).toContain('h-10')
    // destructive 覆盖 default 的主色。
    expect(cls).not.toContain('bg-primary')
  })

  it('outline/ghost/link/secondary 变体语义正确', () => {
    const outline = mount(Button, { props: { variant: 'outline' } })
    expect(outline.attributes('class')).toContain('border')
    const ghost = mount(Button, { props: { variant: 'ghost' } })
    expect(ghost.attributes('class')).toContain('hover:bg-accent')
    const link = mount(Button, { props: { variant: 'link' } })
    expect(link.attributes('class')).toContain('underline-offset-4')
    const secondary = mount(Button, { props: { variant: 'secondary' } })
    expect(secondary.attributes('class')).toContain('bg-secondary')
    const sm = mount(Button, { props: { size: 'sm' } })
    expect(sm.attributes('class')).toContain('h-8')
    const icon = mount(Button, { props: { size: 'icon' } })
    expect(icon.attributes('class')).toContain('size-9')
  })

  it('点击触发透传的 onClick 监听', async () => {
    const onClick = vi.fn()
    const wrapper = mount(Button, { attrs: { onClick } })
    await wrapper.trigger('click')
    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('data-test/type/disabled 等属性透传到根按钮', () => {
    const wrapper = mount(Button, {
      attrs: { 'data-test': 'btn', type: 'submit', disabled: true },
    })
    const btn = wrapper.find('[data-test="btn"]')
    expect(btn.exists()).toBe(true)
    expect(btn.attributes('type')).toBe('submit')
    expect(btn.attributes('disabled')).toBeDefined()
  })
})

describe('Input / Textarea', () => {
  it('Input 透传 data-test 与原生属性', () => {
    const wrapper = mount(Input, {
      attrs: { 'data-test': 'email', type: 'email', placeholder: '邮箱' },
    })
    const input = wrapper.find('input[data-test="email"]')
    expect(input.exists()).toBe(true)
    expect(input.attributes('type')).toBe('email')
    expect(input.attributes('placeholder')).toBe('邮箱')
  })

  it('Input 自定义 class 与内置样式经 tailwind-merge 去冲突', () => {
    const wrapper = mount(Input, { attrs: { class: 'h-12' } })
    const cls = wrapper.find('input').attributes('class') ?? ''
    expect(cls).toContain('h-12')
    expect(cls).not.toContain('h-9')
  })

  it('Textarea 透传 data-test 与原生属性', () => {
    const wrapper = mount(Textarea, {
      attrs: { 'data-test': 'memo', placeholder: '备注', rows: 3 },
    })
    const area = wrapper.find('[data-test="memo"]')
    expect(area.exists()).toBe(true)
    expect(area.element.tagName).toBe('TEXTAREA')
    expect(area.attributes('placeholder')).toBe('备注')
    expect((area.element as HTMLTextAreaElement).rows).toBe(3)
  })
})

describe('Badge', () => {
  it('四种 variant 均映射语义 token 类', () => {
    expect(mount(Badge, { props: { variant: 'default' } }).attributes('class')).toContain('bg-primary')
    expect(mount(Badge, { props: { variant: 'secondary' } }).attributes('class')).toContain('bg-secondary')
    expect(mount(Badge, { props: { variant: 'destructive' } }).attributes('class')).toContain('bg-destructive')
    expect(mount(Badge, { props: { variant: 'outline' } }).attributes('class')).toContain('text-foreground')
  })
})

describe('Separator', () => {
  it('默认水平方向,可切换垂直', () => {
    const horizontal = mount(Separator)
    expect(horizontal.attributes('data-orientation')).toBe('horizontal')
    // 非装饰性才渲染 separator 语义角色。
    const vertical = mount(Separator, { props: { orientation: 'vertical', decorative: false } })
    expect(vertical.attributes('data-orientation')).toBe('vertical')
    expect(vertical.attributes('role')).toBe('separator')
  })
})

describe('Card / Alert / Table / Skeleton', () => {
  it('Card 组合渲染完整结构', () => {
    const wrapper = mount({
      setup: () => () => [
        h(Card, {}, {
          default: () => [
            h(CardHeader, {}, { default: () => [h(CardTitle, {}, { default: () => '标题' })] }),
            h(CardContent, {}, { default: () => '正文' }),
          ],
        }),
      ],
    })
    expect(wrapper.find('[data-slot="card"]').exists()).toBe(true)
    expect(wrapper.find('[data-slot="card-title"]').text()).toBe('标题')
    expect(wrapper.find('[data-slot="card-content"]').text()).toBe('正文')
  })

  it('Alert 变体与描述结构', () => {
    const wrapper = mount({
      setup: () => () => h(Alert, { variant: 'destructive' }, {
        default: () => [
          h(AlertTitle, {}, { default: () => '失败' }),
          h(AlertDescription, {}, { default: () => '操作未完成' }),
        ],
      }),
    })
    const alert = wrapper.find('[data-slot="alert"]')
    expect(alert.attributes('role')).toBe('alert')
    expect(alert.attributes('class')).toContain('text-destructive')
    expect(wrapper.find('[data-slot="alert-description"]').text()).toBe('操作未完成')
  })

  it('Table 渲染容器与行结构', () => {
    const wrapper = mount({
      setup: () => () => h(Table, {}, {
        default: () => [
          h(TableHeader, {}, {
            default: () => h(TableRow, {}, { default: () => h(TableHead, {}, { default: () => '列' }) }),
          }),
          h(TableBody, {}, {
            default: () => h(TableRow, {}, { default: () => h(TableCell, {}, { default: () => '值' }) }),
          }),
        ],
      }),
    })
    expect(wrapper.find('[data-slot="table-container"] table[data-slot="table"]').exists()).toBe(true)
    expect(wrapper.find('thead th[data-slot="table-head"]').text()).toBe('列')
    expect(wrapper.find('tbody td[data-slot="table-cell"]').text()).toBe('值')
  })

  it('Skeleton 支持 class 合并', () => {
    const wrapper = mount(Skeleton, { props: { class: 'w-8 h-2' } })
    const cls = wrapper.attributes('class') ?? ''
    expect(cls).toContain('animate-pulse')
    expect(cls).toContain('w-8')
  })
})

describe('Label', () => {
  it('渲染 label 元素并透传 for', () => {
    const wrapper = mount(Label, { props: { for: 'name' } })
    const label = wrapper.find('label')
    expect(label.exists()).toBe(true)
    expect(label.attributes('for')).toBe('name')
    expect(label.attributes('class')).toContain('font-medium')
  })
})
