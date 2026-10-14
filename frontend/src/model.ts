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
export interface Metric { value: NumberValue; request: NumberValue; limit: NumberValue; sampled_at: string | null; quality: string; scope: string; unit: string }
export interface Workload {
  name: string; display_name: string; namespace: string; pod_uid: string; type: string; status: string; age: string
  containers: { name: string; ready: boolean; restarts: number; cpu: Metric; memory: Metric; gpu_request: NumberValue }[]
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
