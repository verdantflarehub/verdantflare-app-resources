import test from 'node:test'
import assert from 'node:assert/strict'
import { createPolling } from '../src/polling.ts'
import { numeric, resourceValue } from '../src/model.ts'
import type { AppSDK, Lifecycle } from '../src/sdk.ts'

function setup() {
  let lifecycle: (event: Lifecycle) => void = () => {}
  let context = () => {}
  const requests: { signal?: AbortSignal; resolve: (result: { status: number; data: unknown; requestId: string }) => void }[] = []
  const timers = new Map<number, () => void>()
  const updates: unknown[] = []
  let serial = 0
  const sdk: AppSDK = {
    version: '1.1.0',
    app: { request: (_input, options) => new Promise(resolve => { requests.push({ signal: options?.signal, resolve }) }) },
    lifecycle: { subscribe: listener => { lifecycle = listener; return () => { lifecycle = () => {} } } },
    context: { subscribe: listener => { context = listener; return () => { context = () => {} } } },
    theme: { get: () => 'light', subscribe: () => () => {} },
  }
  const polling = createPolling(sdk, value => updates.push(value), { schedule: action => { timers.set(++serial, action); return serial }, cancel: handle => timers.delete(handle as number) })
  return { requests, timers, updates, polling, lifecycle: (event: Lifecycle) => lifecycle(event), context: () => context() }
}
const response = { status: 200, requestId: 'fixture', data: { schema_version: 1, observed_at: new Date().toISOString(), hardware_state: 'unavailable', summary: {}, workloads: [], workloads_summary: {} } }
const flush = async () => { await Promise.resolve(); await Promise.resolve() }

test('hidden dashboard aborts pending read and cannot publish its late result', async () => {
  const s = setup(); const pending = s.polling.refresh(); s.lifecycle('hidden')
  assert.equal(s.requests[0].signal?.aborted, true)
  s.requests[0].resolve(response); await pending
  assert.equal(s.updates.length, 0); assert.equal(s.timers.size, 0)
  s.lifecycle('active'); assert.equal(s.requests.length, 2)
  s.requests[1].resolve(response); await flush()
  assert.equal(s.updates.length, 1); assert.equal(s.timers.size, 1)
  s.lifecycle('dispose'); assert.equal(s.timers.size, 0)
  s.lifecycle('active'); assert.equal(s.requests.length, 2)
})

test('context change clears previous tenant data and rejects old response', async () => {
  const s = setup(); const first = s.polling.refresh(); s.context()
  assert.equal(s.requests[0].signal?.aborted, true)
  assert.deepEqual(s.updates[0], { state: 'loading', data: null, message: '' })
  s.requests[0].resolve(response); await first
  assert.equal(s.updates.length, 1)
  s.requests[1].resolve({ status: 403, data: {}, requestId: 'denied' }); await flush()
  assert.equal((s.updates[1] as { data: unknown }).data, null)
  s.polling.dispose()
})

test('failed refresh clears old figures, and multiple refreshes do not overlap', async () => {
  const s = setup(); const first = s.polling.refresh(); await s.polling.refresh()
  assert.equal(s.requests.length, 1)
  s.requests[0].resolve(response); await first
  const next = s.polling.refresh(); s.requests[1].resolve({ status: 503, data: {}, requestId: 'offline' }); await next
  assert.equal((s.updates[1] as { state: string }).state, 'error')
  assert.equal((s.updates[1] as { data: unknown }).data, null)
  s.polling.dispose()
})

test('zero measurements stay zero and stale or unknown values stay unknown regardless of client clock skew', () => {
  assert.equal(numeric(0), '0'); assert.equal(numeric(null), '—'); assert.equal(numeric(Number.NaN), '—')
  const now = Date.now(), metric = { value: 0, request: 0, limit: null, sampled_at: new Date(now + 10000).toISOString(), quality: 'fresh', scope: 'instance_container', unit: 'cores' }
  assert.equal(resourceValue(metric, 'cores'), '0 核')
  assert.equal(resourceValue({ ...metric, quality: 'stale' }, 'cores'), '—')
  assert.equal(resourceValue({ ...metric, quality: 'unavailable' }, 'cores'), '—')
})
