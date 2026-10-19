import test from 'node:test'
import assert from 'node:assert/strict'
import { aggregate, isCurrentWorkload, releaseReason, statusGroup, uuidV7, workloadKey } from '../src/presentation.ts'
import { createReleaseIntents, isReleased } from '../src/release.ts'
import { isWorkspacesResponse } from '../src/model.ts'
import type { Metric, Workload, WorkspaceVolume } from '../src/model.ts'

const metric: Metric = { value: 0, request: 1, limit: null, sampled_at: '2026-10-15T00:00:00Z', quality: 'fresh', scope: 'instance_container', unit: 'cores' }
const workload: Workload = { name: 'app', display_name: 'app', namespace: 'ns', pod_uid: 'pod-1', type: 'infra', status: 'Running', age: '1h',
  containers: [{ name: 'app', ready: true, restarts: 0, cpu: metric, memory: { ...metric, unit: 'bytes' }, gpu_request: 0 }] }
const workspace: WorkspaceVolume = { workspace_id: 'ns/pvc', pvc_name: 'pvc', pv_name: 'pv', namespace: 'ns', pool_name: 'pool', app_id: 'app', app_display_name: 'App', storage_bytes: 10, storage_class: '', access_mode: 'RWO', node_name: '', state: 'retained', allowed_actions: ['release'] }

test('container usage is complete, fresh and correctly attributed; null is never zero', () => {
  assert.equal(aggregate(workload, 'cpu', 'value'), 0)
  assert.equal(aggregate(workload, 'cpu', 'limit'), null)
  for (const change of [{ value: null }, { quality: 'stale' }, { scope: 'node' }, { unit: 'bytes' }]) {
    const w = { ...workload, containers: [...workload.containers, { ...workload.containers[0], name: 'sidecar', cpu: { ...metric, ...change } }] }
    assert.equal(aggregate(w, 'cpu', 'value'), null)
    assert.equal(aggregate(w, 'cpu', 'request'), 2)
  }
})
test('workload statuses are mutually exclusive and preserve other states', () => {
  assert.equal(statusGroup(workload), 'running')
  assert.equal(statusGroup({ ...workload, containers: [{ ...workload.containers[0], ready: false }] }), 'waiting')
  assert.equal(statusGroup({ ...workload, status: 'Pending' }), 'waiting')
  assert.equal(statusGroup({ ...workload, status: 'Failed' }), 'error')
  assert.equal(statusGroup({ ...workload, status: 'Succeeded' }), 'other')
  assert.equal(isCurrentWorkload(workload), true)
  assert.equal(isCurrentWorkload({ ...workload, status: 'Failed' }), true)
  assert.equal(isCurrentWorkload({ ...workload, status: 'Succeeded' }), false)
  assert.notEqual(workloadKey(workload), workloadKey({ ...workload, namespace: 'other' }))
})
test('release requires retained state, server grant and exact PVC confirmation', () => {
  const intents = createReleaseIntents(() => 'key')
  for (const state of ['in_use', 'bound_stopped', 'available'] as const) {
    const w = { ...workspace, state }
    assert.ok(releaseReason(w))
    assert.throws(() => intents.key(w, w.pvc_name))
  }
  assert.throws(() => intents.key({ ...workspace, allowed_actions: [] }, 'pvc'))
  assert.throws(() => intents.key(workspace, ' pvc '))
  assert.equal(intents.key(workspace, 'pvc'), 'key')
})
test('uncertain attempts reuse the original key, isolation reset forgets old intents', () => {
  let serial = 0
  const intents = createReleaseIntents(() => `key-${++serial}`)
  assert.equal(intents.key(workspace, 'pvc'), 'key-1')
  assert.equal(intents.key({ ...workspace }, 'pvc'), 'key-1')
  assert.equal(intents.key({ ...workspace, pv_name: 'replacement' }, 'pvc'), 'key-2')
  intents.clear()
  assert.equal(intents.key(workspace, 'pvc'), 'key-3')
  assert.equal(isReleased({ status: 'released', workspace }, workspace), true)
  assert.equal(isReleased({ status: 'accepted', workspace }, workspace), false)
  assert.equal(isReleased({ status: 'released', workspace: { ...workspace, pvc_name: 'other' } }, workspace), false)
})
test('release keys carry UUIDv7 timestamp and secure variant bits', () => {
  const before = Date.now(), id = uuidV7(), after = Date.now()
  assert.match(id, /^[\da-f]{8}-[\da-f]{4}-7[\da-f]{3}-[89ab][\da-f]{3}-[\da-f]{12}$/)
  const timestamp = parseInt(id.replaceAll('-', '').slice(0, 12), 16)
  assert.ok(timestamp >= before && timestamp <= after)
  assert.notEqual(id, uuidV7())
})
test('malformed workspace grants cannot expose release actions', () => {
  assert.equal(isWorkspacesResponse({ workspaces: [workspace] }), true)
  assert.equal(isWorkspacesResponse({ workspaces: [{ ...workspace, allowed_actions: null }] }), false)
  assert.equal(isWorkspacesResponse({ workspaces: [null] }), false)
})
