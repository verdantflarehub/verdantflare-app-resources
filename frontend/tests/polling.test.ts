import test from 'node:test'
import assert from 'node:assert/strict'
import { computeBackoffMs, createPolling, createScopedPolling } from '../src/polling.ts'
import { isWorkspacesResponse, numeric, parseResourceView, resourceValue } from '../src/model.ts'
import type { AppSDK, Lifecycle } from '../src/sdk.ts'

function setup(random: () => number = () => 0.5) {
  let lifecycle: (event: Lifecycle) => void = () => {}
  let context = () => {}
  let contextRevision = 'scope-1'
  const requests: { signal?: AbortSignal; resolve: (result: { status: number; data: unknown; requestId: string }) => void }[] = []
  const timers = new Map<number, () => void>()
  const delays: number[] = []
  const updates: unknown[] = []
  let serial = 0
  const sdk: AppSDK = {
    version: '1.1.0',
    app: { request: (_input, options) => new Promise(resolve => { requests.push({ signal: options?.signal, resolve }) }) },
    lifecycle: { subscribe: listener => { lifecycle = listener; return () => { lifecycle = () => {} } } },
    context: { get: () => ({ userId: 'user', organizationId: 'org', stationId: 'station', projectId: null, appId: 'resources', installationId: 'install', contextRevision }), subscribe: listener => { context = listener; return () => { context = () => {} } } },
    theme: { get: () => 'light', subscribe: () => () => {} },
  }
  const polling = createPolling(sdk, value => updates.push(value), { schedule: (action, delay) => { delays.push(delay); timers.set(++serial, action); return serial }, cancel: handle => timers.delete(handle as number) }, random)
  return { requests, timers, delays, updates, polling, lifecycle: (event: Lifecycle) => lifecycle(event), context: () => { contextRevision += '-next'; context() }, sameContext: () => context() }
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

test('failed refresh retains scoped last snapshot, and multiple refreshes do not overlap', async () => {
  const s = setup(); const first = s.polling.refresh(); await s.polling.refresh()
  assert.equal(s.requests.length, 1)
  s.requests[0].resolve(response); await first
  const next = s.polling.refresh(); s.requests[1].resolve({ status: 503, data: {}, requestId: 'offline' }); await next
  assert.equal((s.updates[1] as { state: string }).state, 'ready')
  assert.deepEqual((s.updates[1] as { data: unknown }).data, response.data)
  s.polling.dispose()
})

test('consecutive failures apply exponential backoff with ±15% jitter and reset to 3000ms on recovery', async () => {
  assert.equal(computeBackoffMs(1, () => 0), 850)
  assert.equal(computeBackoffMs(1, () => 0.5), 1000)
  assert.equal(computeBackoffMs(1, () => 1), 1150)
  assert.equal(computeBackoffMs(2, () => 0.5), 2000)
  assert.equal(computeBackoffMs(3, () => 0.5), 4000)

  const s = setup(() => 1)
  const first = s.polling.refresh()
  s.requests[0].resolve(response); await first
  assert.equal(s.delays[0], 3000)

  const f1 = s.polling.refresh(); s.requests[1].resolve({ status: 502, data: {}, requestId: 'e1' }); await f1
  assert.equal(s.delays[1], 1150)
  assert.equal((s.updates[1] as { failures?: number; tripped?: boolean }).failures, 1)
  assert.equal((s.updates[1] as { failures?: number; tripped?: boolean }).tripped, false)

  const f2 = s.polling.refresh(); s.requests[2].resolve({ status: 502, data: {}, requestId: 'e2' }); await f2
  assert.equal(s.delays[2], 2300)

  const f3 = s.polling.refresh(); s.requests[3].resolve({ status: 502, data: {}, requestId: 'e3' }); await f3
  assert.equal(s.delays[3], 4600)
  assert.equal((s.updates[3] as { failures?: number; tripped?: boolean; data: unknown }).tripped, true)
  assert.deepEqual((s.updates[3] as { data: unknown }).data, response.data)

  const ok = s.polling.refresh(); s.requests[4].resolve(response); await ok
  assert.equal(s.delays[4], 3000)
  s.polling.dispose()
})

test('zero measurements stay zero and stale or unknown values stay unknown regardless of client clock skew', () => {
  assert.equal(numeric(0), '0'); assert.equal(numeric(null), '—'); assert.equal(numeric(Number.NaN), '—')
  const now = Date.now(), metric = { value: 0, request: 0, limit: null, sampled_at: new Date(now + 10000).toISOString(), quality: 'fresh', scope: 'instance_container', unit: 'cores' }
  assert.equal(resourceValue(metric, 'cores'), '0 核')
  assert.equal(resourceValue({ ...metric, quality: 'stale' }, 'cores'), '—')
  assert.equal(resourceValue({ ...metric, quality: 'unavailable' }, 'cores'), '—')
})

test('parseResourceView and isWorkspacesResponse validate sub-view query and workspace response', () => {
  assert.equal(parseResourceView('/'), 'monitor')
  assert.equal(parseResourceView('/storage'), 'storage')
  assert.equal(parseResourceView('/tasks'), 'tasks')
  assert.equal(parseResourceView('/?view=monitor'), 'monitor')
  assert.equal(parseResourceView('/?view=storage'), 'storage')
  assert.equal(parseResourceView('/?view=tasks'), 'tasks')
  assert.equal(parseResourceView('/?view=unknown'), 'monitor')
  assert.equal(isWorkspacesResponse({ workspaces: [] }), true)
  assert.equal(isWorkspacesResponse({}), false)
})

test('disabled workspace polling starts only on storage and aborts when leaving', async () => {
  let requests = 0, signal: AbortSignal | undefined
  const sdk = { app: { request: (_: unknown, options: { signal?: AbortSignal }) => { requests++; signal = options.signal; return new Promise(() => {}) } },
    lifecycle: { subscribe: () => () => {} }, context: { subscribe: () => () => {} } } as unknown as AppSDK
  const polling = createScopedPolling(sdk, '/api/v1/workspaces', isWorkspacesResponse, () => {}, undefined, false)
  await polling.refresh(); assert.equal(requests, 0)
  polling.setEnabled(true); assert.equal(requests, 1)
  polling.setEnabled(false); assert.equal(signal?.aborted, true)
  polling.dispose(); polling.setEnabled(true); assert.equal(requests, 1)
})

test('permission denial removes last successful snapshot instead of retaining it', async () => {
  const s = setup(), first = s.polling.refresh()
  s.requests[0].resolve(response); await first
  const next = s.polling.refresh(); s.requests[1].resolve({ status: 403, data: {}, requestId: 'denied' }); await next
  assert.equal((s.updates[1] as { data: unknown }).data, null)
  assert.equal((s.updates[1] as { state: string }).state, 'error')
  s.polling.dispose()
})

test('repeated identical host context retains snapshot and pending read across subpages', async () => {
  const s = setup(), first = s.polling.refresh()
  s.sameContext()
  assert.equal(s.requests[0].signal?.aborted, false)
  s.requests[0].resolve(response); await first
  s.sameContext()
  assert.equal(s.updates.length, 1)
  assert.equal(s.requests.length, 1)
  s.polling.dispose()
})

