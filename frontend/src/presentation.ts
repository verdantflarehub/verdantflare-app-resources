import { formatResource, resourceState, valid } from './model.ts'
import type {
  GPUResidencyActionResult,
  GPUSchedulerCard,
  GPUSchedulerLease,
  GPUSchedulerResidency,
  Metric,
  ModelCacheItem,
  Workload,
  WorkspaceVolume,
} from './model.ts'

export function workloadKey(w: Workload) { return `${w.namespace}/${w.pod_uid || w.name}` }
export function workspaceKey(w: WorkspaceVolume) { return `${w.namespace}/${w.pvc_name}` }
export function isCurrentWorkload(w: Workload) { return w.status !== 'Succeeded' }
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

export type ResidencyVisualState =
  | 'S0'
  | 'S1'
  | 'S2'
  | 'S3'
  | 'TRANSITION'
  | 'RECONCILING'
  | 'QUARANTINED'
  | 'UNKNOWN'

export function residencyVisualState(r: GPUSchedulerResidency): ResidencyVisualState {
  if (r.residency_state === 'quarantined') return 'QUARANTINED'
  if (r.residency_state === 'reconciling') return 'RECONCILING'
  if (r.residency_state === 'waking' || r.residency_state === 'sealing' || r.residency_state === 'draining_hard' || r.snapshot_in_progress) {
    return 'TRANSITION'
  }
  if (r.residency_state === 'active_compute' || Boolean(r.active_compute_lease_id)) return 'S0'
  if (
    r.residency_state === 'human_protected' ||
    r.residency_state === 'human_dirty' ||
    r.human_protected ||
    r.gui_connection_alive
  ) {
    return 'S1'
  }
  if (r.residency_state === 'idle_evictable') return 'S2'
  if (r.residency_state === 'zero_vram_standby') return 'S3'
  return 'UNKNOWN'
}

export function canAdminForceDrainResidency(r: GPUSchedulerResidency): boolean {
  const state = residencyVisualState(r)
  if (state === 'S3') return false
  if (r.snapshot_in_progress || r.residency_state === 'sealing') return false
  return true
}

export function residencyBadge(r: GPUSchedulerResidency): { label: string; tone: 'good' | 'warning' | 'danger' | '' } {
  const state = residencyVisualState(r)
  if (state === 'S0') return { label: 'S0 计算执行中（禁常规释放）', tone: 'good' }
  if (state === 'S1') return { label: 'S1 视口使用中（保护）', tone: 'warning' }
  if (state === 'S2') return { label: 'S2 空闲热缓存（可回收）', tone: 'good' }
  if (state === 'S3') return { label: 'S3 零显存休眠', tone: '' }
  if (state === 'QUARANTINED') return { label: 'Quarantined 隔离冻结（需排查/清场）', tone: 'danger' }
  if (state === 'RECONCILING') return { label: 'Reconciling 状态对账中（暂停常规操作）', tone: 'warning' }
  if (state === 'TRANSITION') {
    const sub = r.residency_state === 'waking'
      ? '唤醒加载中'
      : r.residency_state === 'draining_hard'
        ? '强制清场中'
        : '检查点封存中'
    return { label: `转换中 · ${sub}`, tone: 'warning' }
  }
  return { label: `状态未确认（${r.residency_state || 'unknown'}）`, tone: 'warning' }
}

export function residencyAppLabel(appId: string): string {
  const map: Record<string, string> = {
    comfyui: 'ComfyUI',
    blender: 'Blender',
    video: 'Video MCP',
    'video-mcp': 'Video MCP',
    speech: 'Speech MCP',
    'speech-mcp': 'Speech MCP',
    music: 'Music MCP',
    'music-mcp': 'Music MCP',
    image: 'Image MCP',
    'image-mcp': 'Image MCP',
  }
  return map[appId] || appId || '未分类应用'
}

export interface CardAdmissionCheckOptions {
  pollDegraded?: boolean
  nowMs?: number
  maxSampleAgeMs?: number
}

export function cardAdmissionStatus(
  c: GPUSchedulerCard,
  opts?: CardAdmissionCheckOptions,
): { closed: boolean; badgeLabel: string; badgeTone: 'good' | 'warning' | 'danger' } {
  if (opts?.pollDegraded) {
    return {
      closed: true,
      badgeLabel: '刷新异常 · 准入待确认（Fail-Closed）',
      badgeTone: 'warning',
    }
  }
  const maxAgeMs = opts?.maxSampleAgeMs ?? 30_000
  const nowMs = opts?.nowMs ?? Date.now()
  if (c.telemetry_sampled_at) {
    const sampledMs = Date.parse(c.telemetry_sampled_at)
    if (!Number.isFinite(sampledMs) || nowMs - sampledMs > maxAgeMs) {
      return {
        closed: true,
        badgeLabel: '遥测过期 · 准入关闭',
        badgeTone: 'warning',
      }
    }
  }
  if (!c.telemetry_fresh || !c.admission_open) {
    return {
      closed: true,
      badgeLabel: !c.telemetry_sample_id ? '遥测缺失 · 准入关闭' : '遥测过期 · 准入关闭',
      badgeTone: 'warning',
    }
  }
  if (c.quarantined_mib > 0) {
    return { closed: false, badgeLabel: '部分隔离 · 双层同步', badgeTone: 'warning' }
  }
  return { closed: false, badgeLabel: '双层同步', badgeTone: 'good' }
}

export function blockingReasonLabel(reason?: string | null, holderId?: string | null): string {
  const code = (reason || '').trim()
  const baseMap: Record<string, string> = {
    human_protected: '人类活跃视口 Pinned 保护占用，免杀不抢占',
    draining_evictable_peer: '正在可逆卸载空闲热缓存实例腾出显存',
    draining_peer: '正在可逆卸载空闲热缓存实例腾出显存',
    card_committed_saturation: '物理卡承诺账本预算已满，等待持锁任务释放或降级',
    head_of_line_reservation: '队首大任务已锁定整卡预约屏障，禁止无限插队饥饿',
    startup_free_insufficient: '宿主机 NVML 实测空闲显存不足安全启动下限',
    telemetry_stale_fail_closed: '宿主机 GPU 遥测采样过期，Fail-Closed 暂停新准入',
    node_agent_unhealthy: '宿主机 GPU 探针不可达，Fail-Closed 暂停新准入',
    k8s_slot_exhausted: 'Kubernetes 共享 GPU 时间片名额已达配额上限',
    quarantined_vram: '存在未彻底回收的隔离显存，等待人工或节点恢复',
    gpu_scheduler_queue: '等待 GPU 调度账本分配可用预算',
  }
  const desc = baseMap[code] || (code ? `调度阻塞（${code}）` : '等待可用显存预算')
  return holderId ? `${desc}（持有者 ${holderId}）` : desc
}

export function queuedSchedulerLeases(leases: GPUSchedulerLease[]): GPUSchedulerLease[] {
  return leases
    .filter(l => l.state === 'queued' || l.state === 'draining')
    .slice()
    .sort((a, b) => (a.queue_position || 999999) - (b.queue_position || 999999))
}

export function activeComputeLeases(leases: GPUSchedulerLease[]): GPUSchedulerLease[] {
  return leases.filter(l => l.state === 'leased' || l.state === 'running' || l.state === 'legacy_acquired')
}

export function segmentPercent(partMiB: number, totalMiB: number): string {
  if (!valid(partMiB) || !valid(totalMiB) || totalMiB <= 0) return '0%'
  return `${Math.min(100, Math.max(0, (partMiB / totalMiB) * 100)).toFixed(2)}%`
}

export function createResidencyIntents(makeKey = uuidV7) {
  const keys = new Map<string, string>()
  return {
    evictKey(target: GPUSchedulerResidency) {
      if (residencyVisualState(target) !== 'S2' || !target.evictable) throw new Error('EVICT_NOT_ALLOWED')
      if (!target.boot_id || !Number.isInteger(target.gpu_epoch) || target.gpu_epoch < 0 || !Number.isInteger(target.residency_seq) || target.residency_seq < 0) {
        throw new Error('TARGET_VERSION_REQUIRED')
      }
      const identity = `evict:${target.instance_id}:${target.boot_id}:${target.gpu_epoch}:${target.residency_seq}`
      if (!keys.has(identity)) keys.set(identity, makeKey())
      return keys.get(identity)!
    },
    forceDrainKey(target: GPUSchedulerResidency, ack: boolean, confirmInput: string, viewerCanForceDrain: boolean) {
      if (!viewerCanForceDrain) throw new Error('FORCE_DRAIN_FORBIDDEN')
      if (!canAdminForceDrainResidency(target)) throw new Error('ALREADY_ZERO_VRAM')
      if (!target.boot_id || !Number.isInteger(target.gpu_epoch) || target.gpu_epoch < 0 || !Number.isInteger(target.residency_seq) || target.residency_seq < 0) {
        throw new Error('TARGET_VERSION_REQUIRED')
      }
      if (!ack || confirmInput.trim() !== target.instance_id) throw new Error('CONFIRMATION_REQUIRED')
      const identity = `force-drain:${target.instance_id}:${target.boot_id}:${target.gpu_epoch}:${target.residency_seq}`
      if (!keys.has(identity)) keys.set(identity, makeKey())
      return keys.get(identity)!
    },
    clear() {
      keys.clear()
    },
  }
}

export interface ResidencyActionGateContext {
  active: boolean
  pollDegraded: boolean
  snapshotReady: boolean
  viewerCanEvict: boolean
  viewerCanForceDrain: boolean
  cards: GPUSchedulerCard[]
  liveResidencies: GPUSchedulerResidency[]
  nowMs?: number
  maxSampleAgeMs?: number
}

export function residencyTargetCardFresh(
  r: GPUSchedulerResidency,
  cards: GPUSchedulerCard[],
  opts?: CardAdmissionCheckOptions,
): boolean {
  if (!r.gpu_uuid) return false
  const card = cards.find(c => c.gpu_uuid === r.gpu_uuid)
  if (!card) return false
  return !cardAdmissionStatus(card, opts).closed
}

export function residencyEvictBlockReason(
  target: GPUSchedulerResidency | null | undefined,
  ctx: ResidencyActionGateContext,
): string {
  if (!target) return '未选择目标实例'
  if (!ctx.active) return '页面未处于活动状态，已暂停显存变更操作'
  if (ctx.pollDegraded || !ctx.snapshotReady) return '当前 GPU 调度账本未确认，请恢复连接并刷新后重试'
  if (!ctx.viewerCanEvict) return '当前身份无显存释放权限'
  if (
    !target.boot_id ||
    !Number.isInteger(target.gpu_epoch) ||
    target.gpu_epoch < 0 ||
    !Number.isInteger(target.residency_seq) ||
    target.residency_seq < 0
  ) {
    return '目标实例缺少完整驻留世代版本，请刷新账本后重试'
  }
  const live = ctx.liveResidencies.find(item => item.instance_id === target.instance_id)
  if (
    !live ||
    live.boot_id !== target.boot_id ||
    live.gpu_epoch !== target.gpu_epoch ||
    live.residency_seq !== target.residency_seq
  ) {
    return '409 LEASE_VERSION_CONFLICT：实例驻留世代已变更，请刷新账本后重试'
  }
  if (residencyVisualState(live) !== 'S2' || !live.evictable) {
    return '当前驻留状态不可执行常规显存释放'
  }
  if (!residencyTargetCardFresh(live, ctx.cards, {
    pollDegraded: ctx.pollDegraded,
    nowMs: ctx.nowMs,
    maxSampleAgeMs: ctx.maxSampleAgeMs,
  })) {
    return '目标物理卡遥测采样缺失或已过期（Fail-Closed），已暂停显存释放'
  }
  return ''
}

export function residencyForceDrainBlockReason(
  target: GPUSchedulerResidency | null | undefined,
  ctx: ResidencyActionGateContext,
): string {
  if (!target) return '未选择目标实例'
  if (!ctx.viewerCanForceDrain) {
    return '403 FORBIDDEN：当前身份非组织管理员，禁止执行强制清场。'
  }
  if (!ctx.active) {
    return '页面未处于活动状态，请返回当前页面刷新后重试。'
  }
  if (ctx.pollDegraded || !ctx.snapshotReady) {
    return '当前 GPU 调度账本未确认（Fail-Closed），请恢复连接并刷新后重试。'
  }
  if (
    !target.boot_id ||
    !Number.isInteger(target.gpu_epoch) ||
    target.gpu_epoch < 0 ||
    !Number.isInteger(target.residency_seq) ||
    target.residency_seq < 0
  ) {
    return '目标实例缺少完整驻留世代版本，禁止执行未定界强制清场。'
  }
  const live = ctx.liveResidencies.find(item => item.instance_id === target.instance_id)
  if (!live) {
    return '目标常驻实例已不存在，请刷新账本核验。'
  }
  if (
    live.boot_id !== target.boot_id ||
    live.gpu_epoch !== target.gpu_epoch ||
    live.residency_seq !== target.residency_seq
  ) {
    return '409 LEASE_VERSION_CONFLICT：实例驻留世代已变更，请关闭弹窗刷新后重新确认。'
  }
  if (!canAdminForceDrainResidency(live)) {
    return '当前驻留状态不可执行强制清场。'
  }
  if (!residencyTargetCardFresh(live, ctx.cards, {
    pollDegraded: ctx.pollDegraded,
    nowMs: ctx.nowMs,
    maxSampleAgeMs: ctx.maxSampleAgeMs,
  })) {
    return '目标物理卡遥测采样缺失或已过期（Fail-Closed），请等待宿主机采样恢复后重试。'
  }
  return ''
}

export function isResidencyActionSuccess(value: unknown, instanceId: string): value is { result: GPUResidencyActionResult } {
  if (!value || typeof value !== 'object') return false
  const res = (value as { result?: Partial<GPUResidencyActionResult> }).result
  return !!res && res.instance_id === instanceId && typeof res.boot_id === 'string' && res.boot_id.length > 0 &&
    typeof res.residency_state === 'string'
}

export function modelStatusBadge(
  status: string,
  paidAccess = false,
): { label: string; tone: 'good' | 'blue' | 'warning' | 'danger' | '' } {
  if (paidAccess) {
    return { label: '付费禁入 (403)', tone: 'danger' }
  }
  const map: Record<string, { label: string; tone: 'good' | 'blue' | 'warning' | 'danger' | '' }> = {
    ready: { label: '已就绪 (ready)', tone: 'good' },
    downloading: { label: '下载中 (downloading)', tone: 'blue' },
    verifying_sha256: { label: '哈希核验中 (verifying_sha256)', tone: 'blue' },
    queued: { label: '单飞排队 (queued)', tone: 'warning' },
    evicting: { label: '清理中 (evicting)', tone: 'warning' },
    failed: { label: '下载中断 (failed)', tone: 'danger' },
    not_installed: { label: '未缓存 (not_installed)', tone: '' },
  }
  return map[status] || { label: `状态 (${status || 'unknown'})`, tone: 'warning' }
}

export function modelPinScopeLabel(scope: string): string {
  const map: Record<string, string> = {
    published_api: '已发布工作流 API',
    workflow_run: '运行中工作流任务',
    instance_session: '活跃实例会话',
    ensure_inflight: '模型拉取预留锁',
  }
  return map[scope] || scope
}

export function modelPinSummary(item: ModelCacheItem): string {
  if (item.paid_access) return '禁止拉取 · 违反零付费红线'
  const total = item.active_pins + item.reserved_pins
  if (total > 0) {
    const scopes = item.pin_scopes?.length ? item.pin_scopes.map(modelPinScopeLabel).join('、') : '活跃任务或 API'
    return `持有 ${total} 个引用锁（active=${item.active_pins}, reserved=${item.reserved_pins} · ${scopes}）`
  }
  if (item.status === 'ready') return '0 引用锁 · 可安全清理缓存'
  if (item.status === 'not_installed') return '已登记目录 · 由智能体 model.ensure 按需受控拉取'
  if (item.status === 'failed') return item.error_code ? `上次异常 ${item.error_code} · 支持断点续传恢复` : '支持断点续传恢复'
  return '单飞受控状态流转中'
}

export interface ModelEvictGateContext {
  active: boolean
  pollDegraded: boolean
  modelsReady: boolean
  liveModels: ModelCacheItem[]
}

export function modelEvictBlockReason(
  target: ModelCacheItem | null | undefined,
  ctx: ModelEvictGateContext,
): string {
  if (!target) return '未选择目标模型'
  if (!ctx.active) return '页面未处于活动状态，已暂停模型缓存清理操作'
  if (ctx.pollDegraded || !ctx.modelsReady) return '当前模型缓存台账未确认，请恢复连接并刷新后重试'
  const live = ctx.liveModels.find(m => m.sha256 === target.sha256)
  if (!live || live.download_fence !== target.download_fence) {
    return '模型缓存状态已变更，请刷新台账并重新确认'
  }
  if (live.active_pins + live.reserved_pins > 0) {
    return `409 MODEL_IN_USE：存在 ${live.active_pins + live.reserved_pins} 个活跃或预留引用锁，禁止清理`
  }
  if (live.status !== 'ready' && live.status !== 'failed') {
    return '当前状态不可执行缓存清理（仅 ready 或 failed 且 0 引用模型允许清理）'
  }
  return ''
}

export function createModelEvictIntents(makeKey = () => `evict-${uuidV7()}`) {
  const keys = new Map<string, string>()
  return {
    key(target: ModelCacheItem) {
      if (!/^[0-9a-f]{64}$/.test(target.sha256)) throw new Error('INVALID_MODEL_SHA256')
      if ((target.status !== 'ready' && target.status !== 'failed') || target.active_pins + target.reserved_pins > 0) {
        throw new Error('MODEL_EVICT_NOT_ALLOWED')
      }
      const identity = `${target.sha256}:${target.download_fence}`
      if (!keys.has(identity)) keys.set(identity, makeKey())
      return keys.get(identity)!
    },
    clear() {
      keys.clear()
    },
  }
}

export function isModelEvictSuccess(value: unknown, sha256: string): value is { status: string; sha256: string } {
  if (!value || typeof value !== 'object') return false
  const v = value as { status?: string; sha256?: string }
  return typeof v.status === 'string' && ['evicted', 'evicting', 'not_installed'].includes(v.status) && v.sha256 === sha256
}
