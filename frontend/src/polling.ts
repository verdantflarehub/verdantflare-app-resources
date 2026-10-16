import type { AppSDK, Lifecycle } from './sdk.ts'
import { contextScope } from './sdk.ts'
import { isDashboard } from './model.ts'
import type { Dashboard } from './model.ts'

export interface ReadResult<T> {
  state: 'loading' | 'ready' | 'error'
  data: T | null
  message: string
  failures?: number
  backoffMs?: number
  tripped?: boolean
}
export type PollResult = ReadResult<Dashboard>
export interface Timers { schedule(action: () => void, delay: number): unknown; cancel(handle: unknown): void }
const defaults: Timers = { schedule: (action, delay) => setTimeout(action, delay), cancel: handle => clearTimeout(handle as ReturnType<typeof setTimeout>) }

export function computeBackoffMs(failures: number, random: () => number = Math.random): number {
  const step = Math.max(0, Math.min(failures - 1, 5))
  const base = Math.min(30000, 1000 * (2 ** step))
  const r = Math.min(1, Math.max(0, random()))
  const jitterFactor = 0.85 + r * 0.3
  return Math.round(base * jitterFactor)
}

// Only retain the last successful facts within the current authorized scope.
export function createScopedPolling<T>(sdk: AppSDK, path: string, validate: (value: unknown) => value is T,
  update: (result: ReadResult<T>) => void, timers: Timers = defaults, initiallyEnabled = true, random: () => number = Math.random) {
  let state: Lifecycle = 'active', epoch = 0, enabled = initiallyEnabled
  let timer: unknown, pending: AbortController | null = null, last: T | null = null
  let failures = 0, nextDelay = 3000
  let scope = contextScope(sdk)
  function cancel() {
    epoch++
    if (timer !== undefined) timers.cancel(timer)
    timer = undefined
    pending?.abort(); pending = null
  }
  function fail(message: string) {
    failures++
    nextDelay = computeBackoffMs(failures, random)
    update({ state: last ? 'ready' : 'error', data: last, message, failures, backoffMs: nextDelay, tripped: failures >= 3 })
  }
  async function refresh() {
    if (state !== 'active' || !enabled || pending) return
    if (timer !== undefined) timers.cancel(timer)
    timer = undefined
    const current = epoch, controller = new AbortController(); pending = controller
    try {
      const result = await sdk.app.request({ path, method: 'GET' }, { signal: controller.signal })
      if (current !== epoch || state !== 'active') return
      if (result.status === 401 || result.status === 403) {
        last = null; failures = 0; nextDelay = 3000
        update({ state: 'error', data: null, message: '当前身份没有查看这些资源的权限。' })
      } else if (result.status !== 200 || !validate(result.data)) {
        fail('更新失败，请刷新重试。当前保留上次成功读取的记录。')
      } else {
        last = result.data; failures = 0; nextDelay = 3000
        update({ state: 'ready', data: last, message: '' })
      }
    } catch {
      if (current === epoch && state === 'active' && !controller.signal.aborted) fail('连接异常，请刷新重试。当前保留上次成功读取的记录。')
    } finally {
      if (pending === controller) pending = null
      if (current === epoch && state === 'active' && enabled) timer = timers.schedule(() => { void refresh() }, nextDelay)
    }
  }
  let unsubscribeLifecycle = () => {}, unsubscribeContext = () => {}
  function dispose() { state = 'dispose'; last = null; failures = 0; cancel(); unsubscribeLifecycle(); unsubscribeContext() }
  unsubscribeLifecycle = sdk.lifecycle.subscribe(event => {
    if (state === 'dispose') return
    if (event === 'dispose') { dispose(); return }
    state = event; cancel()
    if (event === 'active') void refresh()
  })
  unsubscribeContext = sdk.context.subscribe(() => {
    if (state === 'dispose') return
    const next = contextScope(sdk)
    if (next !== null && next === scope) return
    scope = next
    cancel(); last = null; failures = 0; nextDelay = 3000; update({ state: 'loading', data: null, message: '' })
    if (state === 'active') void refresh()
  })
  function setEnabled(value: boolean) {
    if (state === 'dispose' || enabled === value) return
    enabled = value; cancel()
    if (enabled) void refresh()
  }
  return { refresh, dispose, setEnabled }
}

export function createPolling(sdk: AppSDK, update: (result: PollResult) => void, timers: Timers = defaults, random: () => number = Math.random) {
  return createScopedPolling(sdk, '/api/v1/dashboard', isDashboard, update, timers, true, random)
}
