import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import PasswordInput from './PasswordInput.vue'

describe('PasswordInput', () => {
  it('默认密文,点击眼睛按钮切换明文/密文并翻转 aria-label', async () => {
    const wrapper = mount(PasswordInput, {
      props: { modelValue: '', toggleTestid: 'toggle-password-x' },
      attrs: { 'data-test': 'input-x' },
    })
    const input = wrapper.find('[data-test="input-x"]')
    const toggle = wrapper.find('[data-test="toggle-password-x"]')
    expect(input.attributes('type')).toBe('password')
    expect(toggle.attributes('type')).toBe('button')
    expect(toggle.attributes('aria-label')).toBe('显示密码')
    await toggle.trigger('click')
    expect(input.attributes('type')).toBe('text')
    expect(toggle.attributes('aria-label')).toBe('隐藏密码')
    await toggle.trigger('click')
    expect(input.attributes('type')).toBe('password')
  })

  it('未声明属性(data-test/placeholder/autocomplete)透传到 input,输入时发出 update:modelValue', async () => {
    const wrapper = mount(PasswordInput, {
      props: { modelValue: '', toggleTestid: 'toggle-password-x' },
      attrs: { 'data-test': 'input-x', placeholder: '请输入密码', autocomplete: 'new-password' },
    })
    const input = wrapper.find('[data-test="input-x"]')
    expect(input.attributes('placeholder')).toBe('请输入密码')
    expect(input.attributes('autocomplete')).toBe('new-password')
    expect(input.classes()).toContain('password-input')
    await input.setValue('s3cret')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['s3cret'])
  })

  it('modelValue 变化反映到输入框(编辑回填/重置场景)', async () => {
    const wrapper = mount(PasswordInput, {
      props: { modelValue: 'a', toggleTestid: 'toggle-password-x' },
      attrs: { 'data-test': 'input-x' },
    })
    expect((wrapper.find('[data-test="input-x"]').element as HTMLInputElement).value).toBe('a')
    await wrapper.setProps({ modelValue: 'b' })
    expect((wrapper.find('[data-test="input-x"]').element as HTMLInputElement).value).toBe('b')
  })
})
