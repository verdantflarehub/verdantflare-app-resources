import test from 'node:test'
import assert from 'node:assert/strict'
import {
  aggregate,
  blockingReasonLabel,
  canAdminForceDrainResidency,
  cardAdmissionStatus,
  createModelEvictIntents,
  createResidencyIntents,
  isCurrentWorkload,
  isModelEvictSuccess,
  isResidencyActionSuccess,
  modelEvictBlockReason,
  modelPinSummary,
  modelStatusBadge,
  queuedSchedulerLeases,
  releaseReason,
  residencyBadge,
  residencyEvictBlockReason,
  residencyForceDrainBlockReason,
  residencyTargetCardFresh,
  residencyVisualState,
  statusGroup,
  uuidV7,
  workloadKey,
} from '../src/presentation.ts'
import type { ModelEvictGateContext, ResidencyActionGateContext } from '../src/presentation.ts'
import { createReleaseIntents, isReleased } from '../src/release.ts'
import { formatGiBFromMiB, formatMiB, formatModelBytes, formatSpeedMiBps, isGPUSchedulerResponse, isModelsResponse, isWorkspacesResponse } from '../src/model.ts'
import type { GPUSchedulerCard, GPUSchedulerLease, GPUSchedulerResidency, Metric, ModelCacheItem, Workload, WorkspaceVolume } from '../src/model.ts'

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

const baseCard: GPUSchedulerCard = {
  node_uid: 'node-1',
  node_name: 'verdentflare-5090',
  gpu_uuid: 'GPU-0001',
  gpu_index: 0,
  model_name: 'NVIDIA GeForce RTX 5090',
  total_vram_mib: 32607,
  driver_overhead_mib: 700,
  headroom_mib: 2048,
  pinned_mib: 6144,
  leased_mib: 0,
  evictable_mib: 11800,
  quarantined_mib: 0,
  unattributed_external_mib: 0,
  unmaterialized_budget_mib: 0,
  ledger_uncommitted_mib: 23715,
  measured_free_vram_mib: 14000,
  measured_used_vram_mib: 18607,
  grantable_standard_mib: 11952,
  telemetry_fresh: true,
  admission_open: true,
  telemetry_sample_id: 42,
  fencing_epoch: 1,
  min_fencing_token: 10,
  time_slicing_slots: 4,
  allocated_k8s_slots: 2,
  scaling_up_k8s_slots: 0,
  unscheduled_pending_k8s_slots: 0,
  admission_mode: 'standard',
  state: 'ready',
  state_reason: '',
  last_event_seq: 9,
  last_event_hash: 'abc',
}

const baseResidency: GPUSchedulerResidency = {
  instance_id: 'comfyui-main',
  app_id: 'comfyui',
  node_uid: 'node-1',
  pod_uid: 'pod-1',
  boot_id: 'boot-abc',
  gpu_epoch: 2,
  residency_seq: 5,
  gpu_uuid: 'GPU-0001',
  residency_state: 'idle_evictable',
  human_protected: false,
  gui_connection_alive: false,
  evictable: true,
  dirty: false,
  snapshot_in_progress: false,
  snapshot_required: false,
  blocking_reason: '',
  pinned_budget_mib: 0,
  proven_vram_mib: 11800,
  idle_seconds: 120,
  template_upgraded: true,
}

test('GPU scheduler response guard, dual-layer Fail-Closed status, and S0-S3 residency intents', () => {
  assert.equal(isGPUSchedulerResponse({
    snapshot: {
      station_id: 'st-1',
      app_gpu_quota: 8,
      cutover_completed: true,
      generated_at: '2026-11-15T04:00:00Z',
      cards: [baseCard],
      active_leases: [],
      residencies: [baseResidency],
      recent_events: [],
    },
    viewer_can_force_drain: true,
    allowed_actions: ['evict', 'force_drain'],
  }), true)
  assert.equal(isGPUSchedulerResponse({ snapshot: null, viewer_can_force_drain: true, allowed_actions: [] }), false)

  assert.equal(formatMiB(32607), '32,607 MiB')
  assert.equal(formatGiBFromMiB(32607), '31.8 GiB')
  assert.equal(cardAdmissionStatus(baseCard).closed, false)
  assert.equal(cardAdmissionStatus(baseCard, { pollDegraded: true }).closed, true)
  assert.match(cardAdmissionStatus(baseCard, { pollDegraded: true }).badgeLabel, /Fail-Closed/)
  assert.equal(cardAdmissionStatus(
    { ...baseCard, telemetry_sampled_at: '2026-11-15T04:00:00Z' },
    { nowMs: Date.parse('2026-11-15T04:00:45Z') },
  ).closed, true)
  assert.equal(cardAdmissionStatus(
    { ...baseCard, telemetry_sampled_at: '2026-11-15T04:00:00Z' },
    { nowMs: Date.parse('2026-11-15T04:00:15Z') },
  ).closed, false)
  assert.equal(cardAdmissionStatus({ ...baseCard, telemetry_fresh: false }).closed, true)
  assert.equal(cardAdmissionStatus({ ...baseCard, admission_open: false }).closed, true)

  assert.equal(residencyVisualState(baseResidency), 'S2')
  assert.equal(residencyVisualState({ ...baseResidency, residency_state: 'active_compute', active_compute_lease_id: 'lease-1' }), 'S0')
  assert.equal(residencyVisualState({ ...baseResidency, residency_state: 'human_protected', human_protected: true }), 'S1')
  assert.equal(residencyVisualState({ ...baseResidency, residency_state: 'human_dirty', dirty: true }), 'S1')
  assert.equal(residencyVisualState({ ...baseResidency, residency_state: 'zero_vram_standby', evictable: false }), 'S3')
  assert.equal(residencyVisualState({ ...baseResidency, residency_state: 'waking', evictable: false }), 'TRANSITION')
  assert.equal(residencyVisualState({ ...baseResidency, residency_state: 'sealing', snapshot_in_progress: true, evictable: false }), 'TRANSITION')
  assert.equal(residencyVisualState({ ...baseResidency, residency_state: 'draining_hard', evictable: false }), 'TRANSITION')
  assert.equal(residencyVisualState({ ...baseResidency, residency_state: 'reconciling', evictable: false }), 'RECONCILING')
  assert.equal(residencyVisualState({ ...baseResidency, residency_state: 'quarantined', evictable: false }), 'QUARANTINED')
  assert.equal(residencyVisualState({ ...baseResidency, residency_state: 'mystery_future_state', evictable: false }), 'UNKNOWN')
  assert.equal(residencyBadge({ ...baseResidency, residency_state: 'quarantined', evictable: false }).tone, 'danger')
  assert.doesNotMatch(residencyBadge({ ...baseResidency, residency_state: 'mystery_future_state', evictable: false }).label, /S3|零显存/)
  assert.match(residencyBadge({ ...baseResidency, residency_state: 'human_protected', human_protected: true }).label, /^S1/)

  assert.equal(canAdminForceDrainResidency({ ...baseResidency, residency_state: 'zero_vram_standby', evictable: false }), false)
  assert.equal(canAdminForceDrainResidency({ ...baseResidency, residency_state: 'sealing', snapshot_in_progress: true, evictable: false }), false)
  assert.equal(canAdminForceDrainResidency({ ...baseResidency, residency_state: 'quarantined', evictable: false }), true)
  assert.equal(canAdminForceDrainResidency({ ...baseResidency, residency_state: 'active_compute', active_compute_lease_id: 'lease-1', evictable: false }), true)
  assert.equal(canAdminForceDrainResidency({ ...baseResidency, residency_state: 'human_dirty', dirty: true, evictable: false }), true)

  let n = 0
  const intents = createResidencyIntents(() => `rk-${++n}`)
  assert.equal(intents.evictKey(baseResidency), 'rk-1')
  assert.equal(intents.evictKey({ ...baseResidency }), 'rk-1')
  assert.equal(intents.evictKey({ ...baseResidency, gpu_epoch: baseResidency.gpu_epoch + 1 }), 'rk-2')
  assert.throws(() => intents.evictKey({ ...baseResidency, residency_state: 'human_protected', evictable: false }), /EVICT_NOT_ALLOWED/)
  assert.throws(() => intents.evictKey({ ...baseResidency, residency_state: 'quarantined', evictable: false }), /EVICT_NOT_ALLOWED/)
  assert.throws(() => intents.forceDrainKey(baseResidency, true, 'comfyui-main', false), /FORCE_DRAIN_FORBIDDEN/)
  assert.throws(() => intents.forceDrainKey({ ...baseResidency, residency_state: 'sealing', snapshot_in_progress: true }, true, 'comfyui-main', true), /ALREADY_ZERO_VRAM/)
  assert.throws(() => intents.forceDrainKey(baseResidency, false, 'comfyui-main', true), /CONFIRMATION_REQUIRED/)
  assert.throws(() => intents.forceDrainKey(baseResidency, true, 'wrong-id', true), /CONFIRMATION_REQUIRED/)
  assert.equal(intents.forceDrainKey(baseResidency, true, 'comfyui-main', true), 'rk-3')
  assert.equal(intents.forceDrainKey({ ...baseResidency, gpu_epoch: baseResidency.gpu_epoch + 1 }, true, 'comfyui-main', true), 'rk-4')
  assert.equal(isResidencyActionSuccess({ result: { instance_id: 'comfyui-main', boot_id: 'boot-abc', residency_state: 'zero_vram_standby' } }, 'comfyui-main'), true)

  const leases: GPUSchedulerLease[] = [
    { lease_id: 'l-2', state: 'queued', holder_app_id: 'video', holder_task_id: 't-2', attempt_id: 'a', stage_id: 's', node_uid: 'n', gpu_uuids: [], peak_budget_mib: 20480, startup_free_min_mib: 20480, fencing_token: 2, priority: 'exclusive_batch', queue_position: 2, blocking_reason: 'human_protected', blocking_holder_id: 'blender-1', expires_at: '', version: 1 },
    { lease_id: 'l-1', state: 'draining', holder_app_id: 'video', holder_task_id: 't-1', attempt_id: 'a', stage_id: 's', node_uid: 'n', gpu_uuids: [], peak_budget_mib: 29696, startup_free_min_mib: 29696, fencing_token: 1, priority: 'exclusive_batch', queue_position: 1, blocking_reason: 'draining_evictable_peer', blocking_holder_id: 'comfyui-main', expires_at: '', version: 1 },
    { lease_id: 'l-0', state: 'running', holder_app_id: 'speech', holder_task_id: 't-0', attempt_id: 'a', stage_id: 's', node_uid: 'n', gpu_uuids: ['GPU-0001'], peak_budget_mib: 8192, startup_free_min_mib: 8192, fencing_token: 3, priority: 'shared_slice', blocking_reason: '', expires_at: '', version: 1 },
  ]
  const queued = queuedSchedulerLeases(leases)
  assert.deepEqual(queued.map(x => x.lease_id), ['l-1', 'l-2'])
  assert.match(blockingReasonLabel('human_protected', 'blender-1'), /blender-1/)
})

test('residency action gates block submit and buttons on inactive page, degraded poll, stale sample, or version drift', () => {
  const sampleNow = Date.parse('2026-11-15T04:00:10Z')
  const freshCard: GPUSchedulerCard = {
    ...baseCard,
    telemetry_sampled_at: '2026-11-15T04:00:00Z',
  }
  const s1Residency: GPUSchedulerResidency = {
    ...baseResidency,
    instance_id: 'blender-s1',
    app_id: 'blender',
    residency_state: 'human_protected',
    human_protected: true,
    evictable: false,
  }
  const baseGateCtx: ResidencyActionGateContext = {
    active: true,
    pollDegraded: false,
    snapshotReady: true,
    viewerCanEvict: true,
    viewerCanForceDrain: true,
    cards: [freshCard],
    liveResidencies: [baseResidency, s1Residency],
    nowMs: sampleNow,
  }

  assert.equal(residencyTargetCardFresh(baseResidency, [freshCard], { nowMs: sampleNow }), true)
  assert.equal(residencyTargetCardFresh(baseResidency, [freshCard], { nowMs: Date.parse('2026-11-15T04:00:45Z') }), false)
  assert.equal(residencyTargetCardFresh({ ...baseResidency, gpu_uuid: 'GPU-MISSING' }, [freshCard], { nowMs: sampleNow }), false)

  // Healthy active state allows S2 evict and S1 admin force-drain.
  assert.equal(residencyEvictBlockReason(baseResidency, baseGateCtx), '')
  assert.equal(residencyForceDrainBlockReason(s1Residency, baseGateCtx), '')

  // Inactive page (StationAppIsolationHost active=false) blocks both evict and modal submit.
  assert.match(residencyEvictBlockReason(baseResidency, { ...baseGateCtx, active: false }), /未处于活动状态/)
  assert.match(residencyForceDrainBlockReason(s1Residency, { ...baseGateCtx, active: false }), /未处于活动状态/)

  // Poll degraded or unconfirmed snapshot blocks both evict and force-drain (Fail-Closed).
  assert.match(residencyEvictBlockReason(baseResidency, { ...baseGateCtx, pollDegraded: true }), /未确认/)
  assert.match(residencyForceDrainBlockReason(s1Residency, { ...baseGateCtx, pollDegraded: true }), /Fail-Closed/)
  assert.match(residencyForceDrainBlockReason(s1Residency, { ...baseGateCtx, snapshotReady: false }), /Fail-Closed/)

  // Stale card telemetry (>30s without a poll completing) blocks both actions even if snapshot object was not replaced.
  const staleNow = Date.parse('2026-11-15T04:00:45Z')
  assert.match(residencyEvictBlockReason(baseResidency, { ...baseGateCtx, nowMs: staleNow }), /Fail-Closed/)
  assert.match(residencyForceDrainBlockReason(s1Residency, { ...baseGateCtx, nowMs: staleNow }), /Fail-Closed/)

  // Missing target version fields are rejected both in gate helpers and in createResidencyIntents.
  const intents = createResidencyIntents(() => 'rk-ver')
  assert.throws(() => intents.evictKey({ ...baseResidency, boot_id: '' }), /TARGET_VERSION_REQUIRED/)
  assert.throws(() => intents.forceDrainKey({ ...s1Residency, boot_id: '' }, true, 'blender-s1', true), /TARGET_VERSION_REQUIRED/)
  assert.match(residencyEvictBlockReason({ ...baseResidency, boot_id: '' }, baseGateCtx), /世代版本/)
  assert.match(residencyForceDrainBlockReason({ ...s1Residency, gpu_epoch: -1 }, baseGateCtx), /世代版本/)

  // Live target version mutation while modal is open blocks submit with 409 LEASE_VERSION_CONFLICT.
  const driftedLive: GPUSchedulerResidency = { ...s1Residency, gpu_epoch: s1Residency.gpu_epoch + 1 }
  assert.match(
    residencyForceDrainBlockReason(s1Residency, { ...baseGateCtx, liveResidencies: [baseResidency, driftedLive] }),
    /409 LEASE_VERSION_CONFLICT/,
  )
  const seqDriftedLive: GPUSchedulerResidency = { ...baseResidency, residency_seq: baseResidency.residency_seq + 1 }
  assert.match(
    residencyEvictBlockReason(baseResidency, { ...baseGateCtx, liveResidencies: [seqDriftedLive, s1Residency] }),
    /409 LEASE_VERSION_CONFLICT/,
  )
})

test('seven-state model cache guard, host-path scrub, 409 MODEL_IN_USE pin lock, and idempotent evict intents', () => {
  const sha = 'a'.repeat(64)
  const baseModel: ModelCacheItem = {
    catalog_code: 'MDL-CKPT-0001',
    model_id: 'sdxl-base-1.0',
    display_name: 'SDXL Base 1.0',
    category: 'checkpoint',
    category_dir: 'checkpoints',
    filename: 'sd_xl_base_1.0.safetensors',
    relative_view_path: 'checkpoints/aaaaaaaaaaaa__sd_xl_base_1.0.safetensors',
    sha256: sha,
    size_bytes: 6_938_078_334,
    vram_footprint_mib: 6800,
    license: 'openrail++',
    license_tier: 'open',
    paid_access: false,
    origin_kind: 'public',
    approval_state: 'approved',
    status: 'ready',
    download_fence: 4,
    bytes_downloaded: 6_938_078_334,
    part_bytes_on_disk: 0,
    speed_bps: 0,
    eta_seconds: 0,
    verified_at: '2026-11-15T06:00:00Z',
    active_pins: 0,
    reserved_pins: 0,
    pin_scopes: [],
  }
  const watermark = {
    filesystem_total_bytes: 2 * 1024 ** 4,
    filesystem_free_bytes: 600 * 1024 ** 3,
    cached_ready_bytes: 80 * 1024 ** 3,
    unmaterialized_reserved_bytes: 0,
    reserved_headroom_bytes: 200 * 1024 ** 3,
    max_auto_pull_model_bytes: 45 * 1024 ** 3,
    headroom_compliant: true,
    sampled_at: '2026-11-15T06:00:00Z',
  }

  assert.equal(isModelsResponse({
    schema_version: 1,
    mount_path: '/models',
    watermark,
    models: [baseModel],
    total_count: 1,
  }), true)
  // Reject host path /data/models or traversal in mount_path or relative_view_path.
  assert.equal(isModelsResponse({
    schema_version: 1,
    mount_path: '/data/models',
    watermark,
    models: [baseModel],
    total_count: 1,
  }), false)
  assert.equal(isModelsResponse({
    schema_version: 1,
    mount_path: '/models',
    watermark,
    models: [{ ...baseModel, relative_view_path: '/data/models/blob' }],
    total_count: 1,
  }), false)
  assert.equal(isModelsResponse({
    schema_version: 1,
    mount_path: '/models',
    watermark,
    models: [{ ...baseModel, relative_view_path: '../secret.safetensors' }],
    total_count: 1,
  }), false)

  assert.equal(formatModelBytes(6_938_078_334), '6.46 GiB')
  assert.equal(formatSpeedMiBps(52_428_800), '50.0 MiB/s')
  assert.equal(modelStatusBadge('ready').tone, 'good')
  assert.equal(modelStatusBadge('downloading').tone, 'blue')
  assert.equal(modelStatusBadge('failed').tone, 'danger')
  assert.deepEqual(modelStatusBadge('not_installed', true), { label: '付费禁入 (403)', tone: 'danger' })
  assert.equal(modelPinSummary({ ...baseModel, status: 'not_installed', paid_access: true }), '禁止拉取 · 违反零付费红线')
  assert.match(modelPinSummary({ ...baseModel, active_pins: 1, reserved_pins: 1, pin_scopes: ['published_api'] }), /已发布工作流 API/)

  const gateCtx: ModelEvictGateContext = {
    active: true,
    pollDegraded: false,
    modelsReady: true,
    liveModels: [baseModel],
  }
  assert.equal(modelEvictBlockReason(baseModel, gateCtx), '')
  assert.match(modelEvictBlockReason(baseModel, { ...gateCtx, active: false }), /未处于活动状态/)
  assert.match(modelEvictBlockReason(baseModel, { ...gateCtx, pollDegraded: true }), /未确认/)

  const pinnedModel: ModelCacheItem = { ...baseModel, active_pins: 1, pin_scopes: ['published_api'] }
  assert.match(modelEvictBlockReason(pinnedModel, { ...gateCtx, liveModels: [pinnedModel] }), /409 MODEL_IN_USE/)
  assert.match(modelEvictBlockReason(baseModel, { ...gateCtx, liveModels: [{ ...baseModel, download_fence: 5 }] }), /已变更/)

  let seq = 0
  const evictIntents = createModelEvictIntents(() => `evk-${++seq}`)
  assert.equal(evictIntents.key(baseModel), 'evk-1')
  assert.equal(evictIntents.key({ ...baseModel }), 'evk-1')
  assert.equal(evictIntents.key({ ...baseModel, download_fence: 5 }), 'evk-2')
  const failedResidualModel: ModelCacheItem = {
    ...baseModel,
    status: 'failed',
    download_fence: 6,
    bytes_downloaded: 1_048_576,
    part_bytes_on_disk: 1_048_576,
    error_code: 'MODEL_DOWNLOAD_INTERRUPTED',
  }
  assert.equal(modelEvictBlockReason(failedResidualModel, { ...gateCtx, liveModels: [failedResidualModel] }), '')
  assert.equal(evictIntents.key(failedResidualModel), 'evk-3')
  assert.throws(() => evictIntents.key(pinnedModel), /MODEL_EVICT_NOT_ALLOWED/)
  assert.throws(() => evictIntents.key({ ...baseModel, status: 'downloading' }), /MODEL_EVICT_NOT_ALLOWED/)
  assert.throws(() => evictIntents.key({ ...baseModel, status: 'not_installed' }), /MODEL_EVICT_NOT_ALLOWED/)
  assert.equal(isModelEvictSuccess({ status: 'evicted', sha256: sha }, sha), true)
  assert.equal(isModelEvictSuccess({ status: 'not_installed', sha256: sha }, sha), true)
  assert.equal(isModelEvictSuccess({ status: 'ready', sha256: sha }, sha), false)
})
