import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'

// Mantine needs these browser APIs, which jsdom lacks.
window.matchMedia ??= ((q: string) => ({
  matches: false, media: q, onchange: null,
  addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false,
})) as never
globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as never
Element.prototype.scrollIntoView ??= () => {}

afterEach(() => {
  cleanup()
  localStorage.clear()
})
