export type NumberValue = number | null
export interface GPU {
  index: number; uuid: string; device: string; model_name: string; sampled_at: string; quality: string
  total_vram_mb: NumberValue; used_vram_mb: NumberValue; free_vram_mb: NumberValue
  utilization: NumberValue; temperature_c: NumberValue; power_watts: NumberValue
}
export interface Disk { mountpoint: string; device: string; fstype: string; total_bytes: NumberValue; avail_bytes: NumberValue; used_bytes: NumberValue; used_percent: NumberValue }
export interface Node {
  sampled_at: string; quality: string
  cpu_cores: NumberValue; cpu_utilization_percent: NumberValue; cpu_load1: NumberValue; cpu_load5: NumberValue; cpu_load15: NumberValue
  mem_total_bytes: NumberValue; mem_used_bytes: NumberValue; mem_available_bytes: NumberValue; mem_cached_bytes: NumberValue; mem_buffers_bytes: NumberValue; mem_used_percent: NumberValue
  storage_disks: Disk[] | null
}
export interface Metric { value: NumberValue; request: NumberValue; limit: NumberValue; sampled_at: string | null; quality: string; scope: string; unit: string; reason?: string }
export interface Workload {
  name: string; display_name: string; namespace: string; pod_uid: string; type: string; status: string; age: string
  pod_name?: string; instance_alias?: string
  containers: { name: string; ready: boolean; restarts: number; cpu: Metric; memory: Metric; gpu_request: NumberValue; gpu_limit?: NumberValue }[]
}
export interface Dashboard {
  schema_version: number; observed_at: string; hardware_state: 'fresh' | 'stale' | 'unavailable'
  summary: Record<string, number | string | null>; gpus: GPU[] | null; node: Node | null; workloads: Workload[]
  workloads_summary: { total_pods: number; total_gpu_requested: number; total_cpu_requested: number; total_memory_requested: number }
}
export function isDashboard(value: unknown): value is Dashboard {
  if (!value || typeof value !== 'object') return false
  const v = value as Partial<Dashboard>
  return v.schema_version === 1 && typeof v.observed_at === 'string' && Number.isFinite(Date.parse(v.observed_at))
    && ['fresh','stale','unavailable'].includes(v.hardware_state ?? '') && !!v.summary && Array.isArray(v.workloads) && !!v.workloads_summary
}
export function valid(value: unknown): value is number { return typeof value === 'number' && Number.isFinite(value) && value >= 0 }
export function numeric(value: unknown, digits = 1) { return valid(value) ? value.toLocaleString('zh-CN', { maximumFractionDigits: digits }) : '—' }
export function scaled(value: unknown, factor: number, digits = 1) { return valid(value) ? numeric(value / factor, digits) : '—' }
export function ratio(used: unknown, total: unknown) { return valid(used) && valid(total) && total > 0 ? used / total * 100 : null }
export function meter(value: unknown) { return valid(value) ? { width: `${Math.min(100, value)}%` } : { width: '0%' } }
export function formatResource(value: unknown, unit = 'cores') {
  if (!valid(value)) return '—'
  if (unit === 'cores') return numeric(value, 3) + ' 核'
  const scale = value > 0 ? Math.max(0, Math.min(4, Math.floor(Math.log(value) / Math.log(1024)))) : 0
  return numeric(value / 1024 ** scale, 2) + ' ' + ['B','KiB','MiB','GiB','TiB'][scale]
}
export function resourceState(metric: Metric) {
  if (metric.quality === 'stale') return 'stale'
  return metric.quality === 'fresh' && valid(metric.value) ? 'fresh' : 'unavailable'
}
export function resourceValue(metric: Metric, unit: string) { return resourceState(metric) === 'fresh' ? formatResource(metric.value, unit) : '—' }

export type ResourceView = 'monitor' | 'storage' | 'tasks'

export interface WorkspaceVolume {
  workspace_id: string
  pvc_name: string
  pv_name: string
  namespace: string
  pool_name: string
  app_id: string
  app_display_name: string
  storage_bytes: number
  storage_class: string
  access_mode: string
  node_name: string
  instance_id?: string | null
  instance_name?: string | null
  organization_id?: string | null
  project_id?: string | null
  installation_id?: string | null
  state: 'in_use' | 'bound_stopped' | 'retained' | 'available'
  allowed_actions: string[]
  bound_at?: string | null
  retained_at?: string | null
}

export function isWorkspacesResponse(value: unknown): value is { workspaces: WorkspaceVolume[] } {
  if (!value || typeof value !== 'object') return false
  const rows = (value as { workspaces?: unknown }).workspaces
  return Array.isArray(rows) && rows.every(w => w && typeof w === 'object' &&
    ['workspace_id', 'pvc_name', 'pv_name', 'namespace', 'state'].every(k => typeof w[k] === 'string') &&
    Array.isArray(w.allowed_actions) && w.allowed_actions.every((a: unknown) => typeof a === 'string'))
}

export function parseResourceView(path: string | undefined | null): ResourceView {
  if (!path) return 'monitor'
  const pathname = path.split('?')[0]
  if (pathname === '/storage' || pathname === '/tasks') return pathname.slice(1) as ResourceView
  const qIndex = path.indexOf('?')
  if (qIndex < 0) return 'monitor'
  const params = new URLSearchParams(path.slice(qIndex + 1))
  const v = params.get('view')
  return v === 'storage' || v === 'tasks' ? v : 'monitor'
}

export interface GPUSchedulerCard {
  node_uid: string
  node_name: string
  gpu_uuid: string
  gpu_index: number
  model_name: string
  total_vram_mib: number
  driver_overhead_mib: number
  headroom_mib: number
  pinned_mib: number
  leased_mib: number
  evictable_mib: number
  quarantined_mib: number
  unattributed_external_mib: number
  unmaterialized_budget_mib: number
  ledger_uncommitted_mib: number
  measured_free_vram_mib: number
  measured_used_vram_mib: number
  grantable_standard_mib: number
  telemetry_fresh: boolean
  admission_open: boolean
  telemetry_boot_id?: string
  telemetry_sample_id: number
  telemetry_sampled_at?: string
  fencing_epoch: number
  min_fencing_token: number
  reserved_by_lease_id?: string | null
  time_slicing_slots: number
  allocated_k8s_slots: number
  scaling_up_k8s_slots: number
  unscheduled_pending_k8s_slots: number
  admission_mode: string
  state: string
  state_reason: string
  last_event_seq: number
  last_event_hash: string
}

export interface GPUSchedulerLease {
  lease_id: string
  state: string
  holder_app_id: string
  holder_task_id: string
  attempt_id: string
  stage_id: string
  node_uid: string
  pod_uid?: string
  container_id?: string
  boot_id?: string
  gpu_uuid?: string
  gpu_uuids: string[]
  reserved_gpu_uuids?: string[]
  peak_budget_mib: number
  startup_free_min_mib: number
  fencing_token: number
  priority: string
  queue_position?: number
  blocking_reason: string
  blocking_holder_id?: string
  expires_at: string
  version: number
  ledger_event_seq?: number
}

export interface GPUSchedulerResidency {
  instance_id: string
  app_id: string
  node_uid: string
  pod_uid: string
  boot_id: string
  gpu_epoch: number
  residency_seq: number
  gpu_uuid: string
  residency_state: string
  human_protected: boolean
  gui_connection_alive: boolean
  evictable: boolean
  dirty: boolean
  snapshot_in_progress: boolean
  snapshot_required: boolean
  blocking_reason: string
  pinned_budget_mib: number
  vram_allocated_mib?: number | null
  proven_vram_mib: number
  active_compute_lease_id?: string
  idle_seconds: number
  template_upgraded: boolean
}

export interface GPUSchedulerEvent {
  event_id: number
  node_uid: string
  gpu_uuid: string
  gpu_index: number
  card_event_seq: number
  occurred_at: string
  entity_type: string
  entity_id: string
  from_state: string
  to_state: string
  budget_mib: number
  reason_code: string
}

export interface GPUSchedulerSnapshot {
  station_id: string
  app_gpu_quota: number
  cutover_completed: boolean
  generated_at: string
  cards: GPUSchedulerCard[]
  active_leases: GPUSchedulerLease[]
  residencies: GPUSchedulerResidency[]
  recent_events: GPUSchedulerEvent[]
}

export interface GPUSchedulerResponse {
  snapshot: GPUSchedulerSnapshot
  viewer_can_force_drain: boolean
  allowed_actions: string[]
  request_id?: string
}

export interface GPUResidencyActionResult {
  instance_id: string
  app_id: string
  from_state: string
  residency_state: string
  boot_id: string
  gpu_epoch: number
  residency_seq: number
  reclaimed_vram_mib: number
  ledger_event_seq: number
}

export function isGPUSchedulerResponse(value: unknown): value is GPUSchedulerResponse {
  if (!value || typeof value !== 'object') return false
  const v = value as Partial<GPUSchedulerResponse>
  if (typeof v.viewer_can_force_drain !== 'boolean') return false
  if (!Array.isArray(v.allowed_actions) || !v.allowed_actions.every(a => typeof a === 'string')) return false
  const snap = v.snapshot as Partial<GPUSchedulerSnapshot> | undefined
  if (!snap || typeof snap !== 'object' || typeof snap.generated_at !== 'string') return false
  return Array.isArray(snap.cards) && Array.isArray(snap.active_leases) &&
    Array.isArray(snap.residencies) && Array.isArray(snap.recent_events)
}

export function formatMiB(value: unknown): string {
  return valid(value) ? `${Math.round(value).toLocaleString('en-US')} MiB` : '—'
}

export function formatGiBFromMiB(value: unknown, digits = 1): string {
  return valid(value) ? `${(value / 1024).toFixed(digits)} GiB` : '—'
}

export type StorageSubtab = 'models' | 'workspaces'

export type ModelLifecycleStatus =
  | 'not_installed'
  | 'queued'
  | 'downloading'
  | 'verifying_sha256'
  | 'ready'
  | 'failed'
  | 'evicting'

export interface StorageWatermark {
  filesystem_total_bytes: NumberValue
  filesystem_free_bytes: NumberValue
  cached_ready_bytes: number
  unmaterialized_reserved_bytes: number
  reserved_headroom_bytes: number
  max_auto_pull_model_bytes: number
  headroom_compliant: boolean | null
  sampled_at: string | null
}

export interface ModelCacheItem {
  catalog_code: string
  model_id: string
  display_name: string
  category: string
  category_dir: string
  filename: string
  relative_view_path: string
  sha256: string
  size_bytes: number
  vram_footprint_mib: number
  license: string
  license_tier: string
  paid_access: boolean
  origin_kind: 'public' | 'local' | string
  approval_state: string
  status: ModelLifecycleStatus | string
  download_fence: number
  bytes_downloaded: number
  part_bytes_on_disk: number
  speed_bps: number
  eta_seconds: number | null
  verified_at: string | null
  error_code?: string
  error_message?: string
  active_pins: number
  reserved_pins: number
  pin_scopes: string[]
}

export interface ModelsResponse {
  schema_version: number
  mount_path: string
  watermark: StorageWatermark
  models: ModelCacheItem[]
  total_count: number
  truncated?: boolean
  request_id?: string
}

const VALID_MODEL_SHA256 = /^[0-9a-f]{64}$/

export function isModelsResponse(value: unknown): value is ModelsResponse {
  if (!value || typeof value !== 'object') return false
  const v = value as Partial<ModelsResponse>
  if (v.schema_version !== 1 || v.mount_path !== '/models' || !v.watermark || typeof v.watermark !== 'object') return false
  if (!Array.isArray(v.models) || typeof v.total_count !== 'number') return false
  return v.models.every(item => {
    if (!item || typeof item !== 'object') return false
    const m = item as Partial<ModelCacheItem>
    return typeof m.catalog_code === 'string' &&
      typeof m.model_id === 'string' &&
      typeof m.category === 'string' &&
      typeof m.filename === 'string' &&
      typeof m.relative_view_path === 'string' &&
      !m.relative_view_path.startsWith('/') &&
      !m.relative_view_path.includes('..') &&
      typeof m.sha256 === 'string' &&
      VALID_MODEL_SHA256.test(m.sha256) &&
      typeof m.status === 'string' &&
      typeof m.size_bytes === 'number' &&
      typeof m.active_pins === 'number' &&
      typeof m.reserved_pins === 'number' &&
      Array.isArray(m.pin_scopes) &&
      m.pin_scopes.every(s => typeof s === 'string')
  })
}

export function formatModelBytes(bytes: unknown): string {
  if (!valid(bytes)) return '—'
  const gib = bytes / (1024 ** 3)
  if (gib >= 1) return `${gib.toFixed(2)} GiB`
  const mib = bytes / (1024 ** 2)
  return `${mib.toFixed(1)} MiB`
}

export function formatSpeedMiBps(speedBps: unknown): string {
  if (!valid(speedBps) || speedBps <= 0) return '—'
  return `${(speedBps / (1024 ** 2)).toFixed(1)} MiB/s`
}

