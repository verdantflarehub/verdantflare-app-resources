import type { AppSDK, Lifecycle } from './sdk.ts'
import { isDashboard } from './model.ts'
import type { Dashboard } from './model.ts'

export interface PollResult { state: 'loading' | 'ready' | 'error'; data: Dashboard | null; message: string }
export interface Timers { schedule(action: () => void, delay: number): unknown; cancel(handle: unknown): void }
const defaults: Timers = { schedule: (action, delay) => setTimeout(action, delay), cancel: handle => clearTimeout(handle as ReturnType<typeof setTimeout>) }

// One request at a time; hidden/disposed/context-replaced responses cannot commit.
export function createPolling(sdk: AppSDK, update: (result: PollResult) => void, timers: Timers = defaults) {
  let state: Lifecycle = 'active', epoch = 0
  let timer: unknown, pending: AbortController | null = null
  function cancel() {
    epoch++
    if (timer !== undefined) timers.cancel(timer)
    timer = undefined
    pending?.abort(); pending = null
  }
  async function refresh() {
    if (state !== 'active' || pending) return
    if (timer !== undefined) timers.cancel(timer)
    timer = undefined
    const current = epoch, controller = new AbortController(); pending = controller
    try {
      const result = await sdk.app.request({ path: '/api/v1/dashboard', method: 'GET' }, { signal: controller.signal })
      if (current !== epoch || state !== 'active') return
      if (result.status === 401 || result.status === 403) {
        update({ state: 'error', data: null, message: '当前身份没有查看资源的权限。' })
      } else if (result.status !== 200 || !isDashboard(result.data)) {
        update({ state: 'error', data: null, message: '资源观测暂不可用，请刷新重试。' })
      } else {
        update({ state: 'ready', data: result.data, message: '' })
      }
    } catch {
      if (current === epoch && state === 'active' && !controller.signal.aborted) update({ state: 'error', data: null, message: '资源连接暂不可用，请刷新重试。' })
    } finally {
      if (pending === controller) pending = null
      if (current === epoch && state === 'active') timer = timers.schedule(() => { void refresh() }, 3000)
    }
  }
  let unsubscribeLifecycle = () => {}, unsubscribeContext = () => {}
  function dispose() { state = 'dispose'; cancel(); unsubscribeLifecycle(); unsubscribeContext() }
  unsubscribeLifecycle = sdk.lifecycle.subscribe(event => {
    if (state === 'dispose') return
    if (event === 'dispose') { dispose(); return }
    state = event; cancel()
    if (event === 'active') void refresh()
  })
  unsubscribeContext = sdk.context.subscribe(() => {
    if (state === 'dispose') return
    cancel(); update({ state: 'loading', data: null, message: '' })
    if (state === 'active') void refresh()
  })
  return { refresh, dispose }
}
