import type { AppSDK, Lifecycle } from './sdk.ts'
import { isDashboard } from './model.ts'
import type { Dashboard } from './model.ts'

export interface ReadResult<T> { state: 'loading' | 'ready' | 'error'; data: T | null; message: string }
export type PollResult = ReadResult<Dashboard>
export interface Timers { schedule(action: () => void, delay: number): unknown; cancel(handle: unknown): void }
const defaults: Timers = { schedule: (action, delay) => setTimeout(action, delay), cancel: handle => clearTimeout(handle as ReturnType<typeof setTimeout>) }

// Only retain the last successful facts within the current authorized scope.
export function createScopedPolling<T>(sdk: AppSDK, path: string, validate: (value: unknown) => value is T,
  update: (result: ReadResult<T>) => void, timers: Timers = defaults, initiallyEnabled = true) {
  let state: Lifecycle = 'active', epoch = 0, enabled = initiallyEnabled
  let timer: unknown, pending: AbortController | null = null, last: T | null = null
  function cancel() {
    epoch++
    if (timer !== undefined) timers.cancel(timer)
    timer = undefined
    pending?.abort(); pending = null
  }
  function fail(message: string) { update({ state: last ? 'ready' : 'error', data: last, message }) }
  async function refresh() {
    if (state !== 'active' || !enabled || pending) return
    if (timer !== undefined) timers.cancel(timer)
    timer = undefined
    const current = epoch, controller = new AbortController(); pending = controller
    try {
      const result = await sdk.app.request({ path, method: 'GET' }, { signal: controller.signal })
      if (current !== epoch || state !== 'active') return
      if (result.status === 401 || result.status === 403) {
        last = null
        update({ state: 'error', data: null, message: '当前身份没有查看这些资源的权限。' })
      } else if (result.status !== 200 || !validate(result.data)) {
        fail('更新失败，请刷新重试。当前保留上次成功读取的记录。')
      } else {
        last = result.data
        update({ state: 'ready', data: last, message: '' })
      }
    } catch {
      if (current === epoch && state === 'active' && !controller.signal.aborted) fail('连接异常，请刷新重试。当前保留上次成功读取的记录。')
    } finally {
      if (pending === controller) pending = null
      if (current === epoch && state === 'active' && enabled) timer = timers.schedule(() => { void refresh() }, 3000)
    }
  }
  let unsubscribeLifecycle = () => {}, unsubscribeContext = () => {}
  function dispose() { state = 'dispose'; last = null; cancel(); unsubscribeLifecycle(); unsubscribeContext() }
  unsubscribeLifecycle = sdk.lifecycle.subscribe(event => {
    if (state === 'dispose') return
    if (event === 'dispose') { dispose(); return }
    state = event; cancel()
    if (event === 'active') void refresh()
  })
  unsubscribeContext = sdk.context.subscribe(() => {
    if (state === 'dispose') return
    cancel(); last = null; update({ state: 'loading', data: null, message: '' })
    if (state === 'active') void refresh()
  })
  function setEnabled(value: boolean) {
    if (state === 'dispose' || enabled === value) return
    enabled = value; cancel()
    if (enabled) void refresh()
  }
  return { refresh, dispose, setEnabled }
}

export function createPolling(sdk: AppSDK, update: (result: PollResult) => void, timers: Timers = defaults) {
  return createScopedPolling(sdk, '/api/v1/dashboard', isDashboard, update, timers)
}
