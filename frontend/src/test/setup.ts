// Shared Vitest setup. Naive UI components query matchMedia and friends.
import { vi } from 'vitest'
import { enableAutoUnmount } from '@vue/test-utils'
import { afterEach } from 'vitest'

// Unmount every mounted component (and teleported DOM) after each test so
// teleported modals rendered into <body> do not leak between tests.
enableAutoUnmount(afterEach)

Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  }),
})

// jsdom 没有 ResizeObserver;reka-ui 浮层定位(PopperContent/useSize/Presence)
// 挂载时会创建它,缺桩会让 portal 内容渲染中断。与上方 matchMedia 同理。
if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = class ResizeObserverStub {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver
}

// jsdom 未实现 pointer capture API;reka-ui 触发器(如 SelectTrigger)在
// pointerdown 中会调用 element.hasPointerCapture(...)。
if (typeof Element !== 'undefined' && !Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false
  Element.prototype.releasePointerCapture = () => {}
  Element.prototype.setPointerCapture = () => {}
}

// jsdom does not implement Range.getClientRects. CodeMirror measures text
// asynchronously after unmount/teardown; without this stub those late
// measurements surface as Vitest unhandled errors even when assertions pass.
if (typeof Range !== 'undefined' && !Range.prototype.getClientRects) {
  Range.prototype.getClientRects = () => [] as unknown as DOMRectList
  Range.prototype.getBoundingClientRect = () =>
    ({ x: 0, y: 0, width: 0, height: 0, top: 0, right: 0, bottom: 0, left: 0 }) as DOMRect
}
