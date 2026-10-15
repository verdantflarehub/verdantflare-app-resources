import { formatResource, resourceState, valid } from './model.ts'
import type { Metric, Workload, WorkspaceVolume } from './model.ts'

export function workloadKey(w: Workload) { return `${w.namespace}/${w.pod_uid || w.name}` }
export function workspaceKey(w: WorkspaceVolume) { return `${w.namespace}/${w.pvc_name}` }
export function statusGroup(w: Workload) {
  if (w.status === 'Running') return w.containers.length > 0 && w.containers.every(c => c.ready) ? 'running' : 'waiting'
  if (w.status === 'Pending') return 'waiting'
  if (['Failed', 'CrashLoopBackOff', 'Error', 'ImagePullBackOff', 'OOMKilled'].includes(w.status)) return 'error'
  return 'other'
}
// Missing container observations never contribute a fabricated zero.
export function aggregate(w: Workload, field: 'cpu' | 'memory', kind: 'value' | 'request' | 'limit') {
  const metrics = w.containers.map(c => c[field])
  if (!metrics.length || metrics.some(m => !valid(m[kind]) || (kind === 'value' &&
    (resourceState(m) !== 'fresh' || m.unit !== (field === 'cpu' ? 'cores' : 'bytes') || m.scope !== 'instance_container')))) return null
  return metrics.reduce((sum, m) => sum + m[kind]!, 0)
}
export function allocation(w: Workload, field: 'cpu' | 'memory') {
  const unit = field === 'cpu' ? 'cores' : 'bytes'
  return ['value', 'request', 'limit'].map(kind => {
    const value = aggregate(w, field, kind as 'value' | 'request' | 'limit')
    return value === null ? (kind === 'limit' ? '未设置 / 未提供' : '未提供') : formatResource(value, unit)
  }).join(' / ')
}
export function metricUsage(m: Metric) {
  return resourceState(m) === 'fresh' && m.scope === 'instance_container' ? formatResource(m.value, m.unit) : '未提供'
}
export function releaseReason(w: WorkspaceVolume) {
  if (w.state === 'in_use') return '工作卷正在挂载，不能释放'
  if (w.state === 'bound_stopped') return '请先销毁关联实例；停止实例不会解除绑定'
  if (w.state !== 'retained') return '当前状态不可释放'
  return w.allowed_actions.includes('release') ? '' : '无释放权限'
}
export function uuidV7() {
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  let timestamp = Date.now()
  for (let i = 5; i >= 0; i--) { bytes[i] = timestamp % 256; timestamp = Math.floor(timestamp / 256) }
  bytes[6] = 0x70 | (bytes[6] & 0x0f); bytes[8] = 0x80 | (bytes[8] & 0x3f)
  const h = Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('')
  return `${h.slice(0,8)}-${h.slice(8,12)}-${h.slice(12,16)}-${h.slice(16,20)}-${h.slice(20)}`
}
