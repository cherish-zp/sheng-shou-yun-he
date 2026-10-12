import { describe, expect, it } from 'vitest'
import { prettyJSON, formatTime, displayValue, truncateCell, formatCount } from './format'

describe('prettyJSON', () => {
  it('formats valid JSON with indentation', () => {
    expect(prettyJSON('{"a":1,"b":[1,2]}')).toBe('{\n  "a": 1,\n  "b": [\n    1,\n    2\n  ]\n}')
  })
  it('returns the raw string for invalid JSON', () => {
    expect(prettyJSON('not json')).toBe('not json')
  })
})

describe('formatTime', () => {
  it('formats a unix-ms timestamp', () => {
    expect(formatTime(0)).toBe('-')
    expect(formatTime(1700000000000)).toMatch(/2023|2024|2025/)
  })
})

describe('displayValue', () => {
  it('falls back for empty values', () => {
    expect(displayValue('')).toBe('(empty)')
    expect(displayValue(null)).toBe('(empty)')
    expect(displayValue(undefined)).toBe('(empty)')
    expect(displayValue('hello')).toBe('hello')
  })
})

describe('truncateCell', () => {
  it('marks null/empty as (empty) like displayValue and passes blanks through untrimmed', () => {
    expect(truncateCell(null)).toBe('(empty)')
    expect(truncateCell(undefined)).toBe('(empty)')
    expect(truncateCell('')).toBe('(empty)')
    expect(truncateCell('  ')).toBe('  ')
  })
  it('passes short values through verbatim', () => {
    expect(truncateCell('hello')).toBe('hello')
    expect(truncateCell('x'.repeat(200))).toBe('x'.repeat(200))
  })
  it('truncates overlong values to 200 chars with an ellipsis', () => {
    expect(truncateCell('x'.repeat(201))).toBe(`${'x'.repeat(200)}…`)
    expect(truncateCell('x'.repeat(1000))).toBe(`${'x'.repeat(200)}…`)
  })
  it('does not touch the untruncated displayValue', () => {
    // 详情抽屉仍走 displayValue:完整值语义不被截断函数影响。
    expect(displayValue('x'.repeat(1000))).toBe('x'.repeat(1000))
  })
})

describe('formatCount', () => {
  it('renders small counts verbatim', () => {
    expect(formatCount(0)).toBe('0')
    expect(formatCount(999)).toBe('999')
  })
  it('abbreviates thousands, millions and billions', () => {
    expect(formatCount(1200)).toBe('1.2K')
    expect(formatCount(35600)).toBe('35.6K')
    expect(formatCount(1200000)).toBe('1.2M')
    expect(formatCount(4500000000)).toBe('4.5B')
  })
  it('drops trailing zeros', () => {
    expect(formatCount(3000)).toBe('3K')
    expect(formatCount(1000000)).toBe('1M')
  })
  it('handles negatives without crashing', () => {
    expect(formatCount(-5)).toBe('-5')
  })
})
