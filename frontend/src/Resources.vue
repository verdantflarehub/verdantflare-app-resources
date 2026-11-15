<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import ResourceIcon from './ResourceIcon.vue'
import { contextScope, hostProps } from './sdk'
import type { AppSDK, DashboardHost } from './sdk'
import { createPolling, createScopedPolling } from './polling'
import {
  formatGiBFromMiB,
  formatMiB,
  formatModelBytes,
  formatResource,
  formatSpeedMiBps,
  isGPUSchedulerResponse,
  isModelsResponse,
  isWorkspacesResponse,
  meter,
  numeric,
  parseResourceView,
  ratio,
  scaled,
  valid,
} from './model'
import type {
  Dashboard,
  GPUSchedulerCard,
  GPUSchedulerLease,
  GPUSchedulerResidency,
  GPUSchedulerResponse,
  ModelCacheItem,
  ModelsResponse,
  ResourceView,
  StorageSubtab,
  WorkspaceVolume,
  Workload,
} from './model'
import {
  activeComputeLeases,
  allocation,
  blockingReasonLabel,
  canAdminForceDrainResidency,
  cardAdmissionStatus,
  createModelEvictIntents,
  createResidencyIntents,
  isCurrentWorkload,
  isModelEvictSuccess,
  isResidencyActionSuccess,
  metricUsage,
  modelEvictBlockReason,
  modelPinSummary,
  modelStatusBadge,
  queuedSchedulerLeases,
  releaseReason,
  residencyAppLabel,
  residencyBadge,
  residencyEvictBlockReason,
  residencyForceDrainBlockReason,
  residencyTargetCardFresh,
  residencyVisualState,
  segmentPercent,
  statusGroup,
  workloadKey,
  workspaceKey,
} from './presentation'
import { createReleaseIntents, isReleased } from './release'

const dashboard = ref<Dashboard | null>(null), state = ref<'loading' | 'ready' | 'error'>('loading'), error = ref('')
const pollFailures = ref(0), pollBackoffMs = ref(0), pollTripped = ref(false)
const currentView = ref<ResourceView>('monitor'), refreshing = ref(false), toast = ref('')
const volumes = ref<WorkspaceVolume[] | null>(null), volumeState = ref<'loading' | 'ready' | 'error'>('loading'), volumeError = ref('')
const volumeFailures = ref(0), volumeBackoffMs = ref(0), volumeTripped = ref(false)
const gpuScheduler = ref<GPUSchedulerResponse | null>(null), gpuSchedulerState = ref<'loading' | 'ready' | 'error'>('loading'), gpuSchedulerError = ref('')
const gpuSchedulerFailures = ref(0), gpuSchedulerBackoffMs = ref(0), gpuSchedulerTripped = ref(false)
const storageSubtab = ref<StorageSubtab>('models')
const modelsData = ref<ModelsResponse | null>(null), modelState = ref<'loading' | 'ready' | 'error'>('loading'), modelError = ref('')
const modelFailures = ref(0), modelBackoffMs = ref(0), modelTripped = ref(false)
const modelSearch = ref(''), modelCategory = ref('all'), modelStatusFilter = ref('all'), modelOriginFilter = ref('all')
const volumeSearch = ref(''), volumeFilter = ref('all'), taskSearch = ref(''), taskFilter = ref('all'), taskStatus = ref('all')
const expandedGPU = ref(new Set<string>()), expandedHost = ref(false), expandedVolume = ref(new Set<string>()), expandedModel = ref(new Set<string>()), expandedTask = ref(new Set<string>()), expandedAuditEvents = ref(false)
const meta = {
  monitor: { title: '资源监控', description: '查看 GPU、CPU、内存与存储使用情况。' },
  storage: { title: '存储管理', description: '查看磁盘容量、共享模型缓存库（CAS 只读视图 /models:ro）与应用工作卷池。' },
  tasks: { title: '任务监控', description: '查看物理 GPU 双层显存账本、排队阻塞原因、常驻平台 S0–S3 驻留状态与容器工作负载。' },
}
const summary = computed(() => dashboard.value?.summary ?? {}), node = computed(() => dashboard.value?.node)
const gpus = computed(() => dashboard.value?.gpus ?? []), disks = computed(() => node.value?.storage_disks ?? [])
const workloads = computed(() => (dashboard.value?.workloads ?? []).filter(isCurrentWorkload))
const groups = { running: '运行中', waiting: '等待就绪', error: '异常', other: '其他状态' }
const taskCounts = computed(() => {
  const counts = { running: 0, waiting: 0, error: 0, other: 0 }
  for (const w of workloads.value) counts[statusGroup(w)]++
  return counts
})
// The current server only classifies GPU allocation versus infrastructure. Do not guess business categories from names.
const categories = computed(() => Array.from(new Set(workloads.value.map(w => w.type || 'unknown'))))
function categoryLabel(type: string) { return type === 'gpu' ? 'GPU 工作负载' : type === 'infra' ? '基础工作负载（未分类）' : '未分类' }
const visibleTasks = computed(() => workloads.value.filter(w =>
  (taskFilter.value === 'all' || (w.type || 'unknown') === taskFilter.value) &&
  (taskStatus.value === 'all' || statusGroup(w) === taskStatus.value) &&
  `${w.name} ${w.display_name} ${w.namespace} ${w.pod_name ?? ''}`.toLocaleLowerCase().includes(taskSearch.value.trim().toLocaleLowerCase())))
const volumeLabels: Record<string, string> = { in_use: '挂载运行中', bound_stopped: '已停止绑定', retained: '待释放', available: '可分配' }
const volumeCounts = computed(() => {
  const counts: Record<string, number> = { all: volumes.value?.length ?? 0, in_use: 0, bound_stopped: 0, retained: 0 }
  for (const w of volumes.value ?? []) counts[w.state] = (counts[w.state] ?? 0) + 1
  return counts
})
const visibleVolumes = computed(() => (volumes.value ?? []).filter(w =>
  (volumeFilter.value === 'all' || w.state === volumeFilter.value) &&
  `${w.pvc_name} ${w.pv_name} ${w.app_display_name} ${w.app_id} ${w.pool_name} ${w.instance_name ?? ''} ${w.instance_id ?? ''} ${w.namespace}`
    .toLocaleLowerCase().includes(volumeSearch.value.trim().toLocaleLowerCase())))
const hardwareNotice = computed(() => dashboard.value?.hardware_state === 'stale' ? '硬件采样已过期，等待服务端新观测。' :
  dashboard.value?.hardware_state === 'unavailable' ? '硬件观测暂不可用；缺失值不代表零用量。' :
    summary.value.status === 'Partial' ? '部分硬件观测缺失，当前汇总不完整。' : '')
function sampleTime(value?: string | null) { return value && Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '未提供' }
function quality(value?: string) { return value === 'fresh' ? '数据正常' : value === 'stale' ? '采样过期' : '采样不可用' }
function gpuKey(g: { uuid: string; device: string; index: number }) { return g.uuid || g.device || String(g.index) }
function toggle(set: Set<string>, key: string) { if (set.has(key)) set.delete(key); else set.add(key) }
function clearVolumes() { volumeSearch.value = ''; volumeFilter.value = 'all' }
function clearModels() { modelSearch.value = ''; modelCategory.value = 'all'; modelStatusFilter.value = 'all'; modelOriginFilter.value = 'all' }
function clearTasks() { taskSearch.value = ''; taskFilter.value = 'all'; taskStatus.value = 'all' }
function gpuDeclaration(w: Workload) {
  return w.containers.length && w.containers.every(c => valid(c.gpu_request)) ? numeric(w.containers.reduce((s,c) => s + c.gpu_request!, 0)) + ' 份' : '未提供'
}

let sdk: AppSDK | undefined, host: DashboardHost | undefined, polling: ReturnType<typeof createPolling> | undefined
let volumePolling: ReturnType<typeof createScopedPolling<{ workspaces: WorkspaceVolume[] }>> | undefined
let modelPolling: ReturnType<typeof createScopedPolling<ModelsResponse>> | undefined
let gpuSchedulerPolling: ReturnType<typeof createScopedPolling<GPUSchedulerResponse>> | undefined
let observer: ResizeObserver | undefined, disposed = false, active = true, scopeEpoch = 0
const pageActive = ref(true), sampleClockMs = ref(Date.now())
let sampleClockTimer: ReturnType<typeof setInterval> | undefined
let scope: string | null = null
const subscriptions: (() => void)[] = [], scrolls: Record<ResourceView, number> = { monitor: 0, storage: 0, tasks: 0 }
let restoreFrame = 0

const schedulerSnapshot = computed(() => gpuScheduler.value?.snapshot ?? null)
const schedulerPollDegraded = computed(() => Boolean(gpuSchedulerError.value) || gpuSchedulerFailures.value > 0 || gpuSchedulerState.value === 'error')
const schedulerCards = computed(() => schedulerSnapshot.value?.cards ?? [])
const schedulerLeases = computed(() => schedulerSnapshot.value?.active_leases ?? [])
const queuedLeases = computed(() => queuedSchedulerLeases(schedulerLeases.value))
const runningLeases = computed(() => activeComputeLeases(schedulerLeases.value))
const schedulerResidencies = computed(() => schedulerSnapshot.value?.residencies ?? [])
const schedulerEvents = computed(() => schedulerSnapshot.value?.recent_events ?? [])
const evictableResidencies = computed(() => schedulerResidencies.value.filter(r => residencyVisualState(r) === 'S2' && r.evictable))
const viewerCanEvict = computed(() => Boolean(gpuScheduler.value?.allowed_actions.includes('evict')))
const viewerCanForceDrain = computed(() => Boolean(gpuScheduler.value?.viewer_can_force_drain && gpuScheduler.value?.allowed_actions.includes('force_drain')))
const totalCommittedMiB = computed(() => schedulerCards.value.reduce((sum, c) => sum + c.pinned_mib + c.leased_mib + c.quarantined_mib, 0))
const totalSchedulerCapacityMiB = computed(() => schedulerCards.value.reduce((sum, c) => sum + c.total_vram_mib, 0))
const totalEvictableMiB = computed(() => schedulerCards.value.reduce((sum, c) => sum + c.evictable_mib, 0))
const maxLedgerSeq = computed(() => schedulerCards.value.reduce((max, c) => Math.max(max, c.last_event_seq), 0))
const totalK8sSlotsUsed = computed(() => schedulerCards.value.reduce((sum, c) => sum + c.allocated_k8s_slots + c.scaling_up_k8s_slots, 0))

function isResidencyPageActive(): boolean {
  return Boolean(active && pageActive.value && !disposed)
}
function residencyGateContext(nowOverride?: number) {
  return {
    active: isResidencyPageActive(),
    pollDegraded: schedulerPollDegraded.value,
    snapshotReady: Boolean(schedulerSnapshot.value),
    viewerCanEvict: viewerCanEvict.value,
    viewerCanForceDrain: viewerCanForceDrain.value,
    cards: schedulerCards.value,
    liveResidencies: schedulerResidencies.value,
    nowMs: nowOverride ?? sampleClockMs.value,
  }
}
function evictBlockReason(r: GPUSchedulerResidency | null | undefined, nowOverride?: number): string {
  return residencyEvictBlockReason(r, residencyGateContext(nowOverride))
}
function forceDrainBlockReason(r: GPUSchedulerResidency | null | undefined, nowOverride?: number): string {
  return residencyForceDrainBlockReason(r, residencyGateContext(nowOverride))
}
function isResidencyCardFresh(r: GPUSchedulerResidency, nowOverride?: number): boolean {
  return residencyTargetCardFresh(r, schedulerCards.value, {
    pollDegraded: schedulerPollDegraded.value,
    nowMs: nowOverride ?? sampleClockMs.value,
  })
}
const actionableEvictableResidencies = computed(() => evictableResidencies.value.filter(r => !evictBlockReason(r)))

function cardAdmission(c: GPUSchedulerCard) {
  return cardAdmissionStatus(c, { pollDegraded: schedulerPollDegraded.value, nowMs: sampleClockMs.value })
}
function cardByUUID(gpuUuid: string): GPUSchedulerCard | undefined {
  return schedulerCards.value.find(c => c.gpu_uuid === gpuUuid)
}
function residencyCardLabel(r: GPUSchedulerResidency): string {
  if (residencyVisualState(r) === 'S3') return '—（零显存待命）'
  const c = cardByUUID(r.gpu_uuid)
  return c ? `GPU ${c.gpu_index}` : (r.gpu_uuid || '未提供')
}
function residencyCardSub(r: GPUSchedulerResidency): string {
  const s = residencyVisualState(r)
  if (s === 'S0') return '计算租约执行中'
  if (s === 'S1') return 'WebRTC 心跳在线'
  if (s === 'S2') return '空闲热缓存可驱逐'
  if (s === 'S3') return '零 CUDA 上下文'
  if (s === 'QUARANTINED') return '显存回收异常已隔离'
  if (s === 'RECONCILING') return '守护状态对账中'
  if (s === 'TRANSITION') return '状态转换执行中'
  return '驻留状态待确认'
}
function residencyCommittedMiB(r: GPUSchedulerResidency): number {
  const s = residencyVisualState(r)
  if (s === 'S3') return 0
  if (s === 'S0' && r.active_compute_lease_id) {
    const lease = schedulerLeases.value.find(l => l.lease_id === r.active_compute_lease_id)
    if (lease) return r.pinned_budget_mib + lease.peak_budget_mib
  }
  return r.pinned_budget_mib
}
function residencyMeasuredMiB(r: GPUSchedulerResidency): number {
  if (residencyVisualState(r) === 'S3') return 0
  if (r.proven_vram_mib > 0) return r.proven_vram_mib
  return valid(r.vram_allocated_mib) ? r.vram_allocated_mib : 0
}
function residencyNote(r: GPUSchedulerResidency): string {
  const s = residencyVisualState(r)
  if (s === 'S0') return `持有计算租约 ${r.active_compute_lease_id || ''} · 任务结项后自动退回 S2`
  if (s === 'S1') return '活跃人类视口免杀保护 · 常规释放与后台抢占已阻断'
  if (s === 'S2') return `空闲 ${r.idle_seconds}s · 可逆 evict-vram 卸载保留外层 boot_id`
  if (s === 'S3') return `零显存休眠待命 · 外层 boot_id=${r.boot_id} 保持恒定`
  if (s === 'QUARANTINED') return `隔离原因 ${r.blocking_reason || 'RECLAIM_FAILED_QUARANTINED'} · 仅允许管理员强制清场或排障恢复`
  if (s === 'RECONCILING') return '外层守护进程正在与 NVML/账本对账 · 常规释放已暂停'
  if (s === 'TRANSITION') return `当前处于 ${r.residency_state} 转换态 · 请等待完成后再执行常规释放`
  return `未知驻留状态 ${r.residency_state || 'unknown'} · Fail-Closed 禁止当作零显存`
}
function leaseTargetCardLabel(l: GPUSchedulerLease): string {
  const uuids = l.gpu_uuids?.length ? l.gpu_uuids : (l.reserved_gpu_uuids ?? (l.gpu_uuid ? [l.gpu_uuid] : []))
  if (!uuids.length) return '待分配物理卡'
  return uuids.map(u => {
    const c = cardByUUID(u)
    return c ? `GPU ${c.gpu_index}` : u
  }).join(' + ')
}
function cardActiveUsedMiB(c: GPUSchedulerCard): number {
  return Math.max(0, c.measured_used_vram_mib - c.evictable_mib)
}

const residencyIntents = createResidencyIntents()
const evictingIds = ref(new Set<string>()), reclaimingAll = ref(false)
const blockedEvictDialog = ref<HTMLDialogElement>(), blockedEvictTarget = ref<GPUSchedulerResidency | null>(null)
const forceDrainDialog = ref<HTMLDialogElement>(), forceDrainTarget = ref<GPUSchedulerResidency | null>(null)
const forceDrainAck = ref(false), forceDrainInput = ref(''), forceDrainError = ref(''), forceDraining = ref(false)
const forceDrainBlock = computed(() => forceDrainTarget.value ? forceDrainBlockReason(forceDrainTarget.value) : '')

function openBlockedEvict(r: GPUSchedulerResidency) {
  if (!isResidencyPageActive() || schedulerPollDegraded.value || !isResidencyCardFresh(r, Date.now())) return
  blockedEvictTarget.value = { ...r }
  blockedEvictDialog.value?.showModal()
}
function closeBlockedEvict() {
  blockedEvictDialog.value?.close()
  blockedEvictTarget.value = null
}
async function evictResidency(r: GPUSchedulerResidency, silent = false): Promise<boolean> {
  const nowMs = Date.now()
  sampleClockMs.value = nowMs
  if (!sdk || !isResidencyPageActive() || schedulerPollDegraded.value || !viewerCanEvict.value || evictingIds.value.has(r.instance_id)) return false
  if (residencyVisualState(r) === 'S1' && isResidencyCardFresh(r, nowMs)) {
    openBlockedEvict(r)
    return false
  }
  const block = evictBlockReason(r, nowMs)
  if (block) {
    if (!silent) toast.value = block
    return false
  }
  const epoch = scopeEpoch
  let idempotencyKey: string
  try {
    idempotencyKey = residencyIntents.evictKey(r)
  } catch {
    return false
  }
  evictingIds.value.add(r.instance_id)
  try {
    const result = await sdk.app.request({
      path: '/api/v1/gpu-residency/evict',
      method: 'POST',
      body: {
        idempotency_key: idempotencyKey,
        instance_id: r.instance_id,
        expected_boot_id: r.boot_id,
        expected_gpu_epoch: r.gpu_epoch,
        expected_residency_seq: r.residency_seq,
      },
    })
    if (epoch !== scopeEpoch || disposed) return false
    if (result.status !== 200 || !isResidencyActionSuccess(result.data, r.instance_id)) {
      const code = (result.data as { code?: string } | null)?.code
      await gpuSchedulerPolling?.refresh()
      if (code === 'GUI_EDIT_LEASE_HELD') {
        openBlockedEvict(r)
      } else if (!silent) {
        toast.value = code === 'INSTANCE_ACTIVE_COMPUTE'
          ? `${r.instance_id} 正处于 S0 计算执行中，常规释放已被拦截。`
          : code === 'ALREADY_ZERO_VRAM'
            ? `${r.instance_id} 已处于 S3 零显存待命状态。`
            : code === 'LEASE_VERSION_CONFLICT'
              ? `${r.instance_id} 驻留世代已变更，已自动刷新账本，请核对后重试。`
              : code === 'RECLAIM_FAILED_QUARANTINED'
                ? `${r.instance_id} 显存回收校验未通过，已隔离冻结（503 RECLAIM_FAILED_QUARANTINED）。`
                : `显存释放结果待确认（${code || result.status}），已刷新账本。`
      }
      return false
    }
    const action = result.data.result
    if (!silent) {
      await Promise.all([gpuSchedulerPolling?.refresh(), polling?.refresh()])
      toast.value = `${action.instance_id} 已可逆卸载至 S3（释放 ${formatGiBFromMiB(action.reclaimed_vram_mib)}，外层 boot_id=${action.boot_id} 保持不变）。`
    }
    return true
  } catch {
    if (epoch === scopeEpoch && !disposed && !silent) {
      toast.value = '连接异常，显存释放结果待确认；重试将复用原幂等键。'
    }
    return false
  } finally {
    evictingIds.value.delete(r.instance_id)
  }
}
async function reclaimAllEvictable() {
  sampleClockMs.value = Date.now()
  const candidates = [...actionableEvictableResidencies.value]
  if (!sdk || !isResidencyPageActive() || schedulerPollDegraded.value || reclaimingAll.value || !candidates.length) return
  reclaimingAll.value = true
  let count = 0
  try {
    for (const r of candidates) {
      if (!isResidencyPageActive() || schedulerPollDegraded.value) break
      const ok = await evictResidency(r, true)
      if (ok) count++
    }
    await Promise.all([gpuSchedulerPolling?.refresh(), polling?.refresh()])
    if (count > 0) {
      toast.value = `已完成 ${count} 个 S2 空闲实例显存回收，并触发排队队列重评估。`
    }
  } finally {
    reclaimingAll.value = false
  }
}
function openForceDrain(r: GPUSchedulerResidency) {
  sampleClockMs.value = Date.now()
  if (forceDrainBlockReason(r, sampleClockMs.value)) return
  forceDrainTarget.value = { ...r }
  forceDrainAck.value = false
  forceDrainInput.value = ''
  forceDrainError.value = ''
  forceDrainDialog.value?.showModal()
}
function closeForceDrain() {
  if (forceDraining.value) return
  forceDrainDialog.value?.close()
  forceDrainTarget.value = null
  forceDrainAck.value = false
  forceDrainInput.value = ''
  forceDrainError.value = ''
}
async function submitForceDrain() {
  const target = forceDrainTarget.value
  if (!target || !sdk || forceDraining.value) return
  const nowMs = Date.now()
  sampleClockMs.value = nowMs
  const block = forceDrainBlockReason(target, nowMs)
  if (block) {
    forceDrainError.value = block
    return
  }
  if (!forceDrainAck.value || forceDrainInput.value.trim() !== target.instance_id) {
    forceDrainError.value = `400 CONFIRMATION_REQUIRED：必须勾选风险确认框并精确输入实例标识 "${target.instance_id}"。`
    return
  }
  const epoch = scopeEpoch
  let idempotencyKey: string
  try {
    idempotencyKey = residencyIntents.forceDrainKey(target, forceDrainAck.value, forceDrainInput.value, viewerCanForceDrain.value)
  } catch {
    forceDrainError.value = `400 CONFIRMATION_REQUIRED：必须勾选风险确认框并精确输入实例标识 "${target.instance_id}"。`
    return
  }
  forceDraining.value = true
  forceDrainError.value = ''
  try {
    const result = await sdk.app.request({
      path: '/api/v1/gpu-residency/force-drain',
      method: 'POST',
      body: {
        idempotency_key: idempotencyKey,
        instance_id: target.instance_id,
        confirm_force_drain: forceDrainAck.value,
        confirm_instance_id: forceDrainInput.value.trim(),
        expected_boot_id: target.boot_id,
        expected_gpu_epoch: target.gpu_epoch,
        expected_residency_seq: target.residency_seq,
      },
    })
    if (epoch !== scopeEpoch || disposed) return
    if (result.status !== 200 || !isResidencyActionSuccess(result.data, target.instance_id)) {
      const code = (result.data as { code?: string } | null)?.code
      forceDrainError.value = result.status === 403 ? '403 FORBIDDEN：当前身份没有管理员强制清场权限。' :
        code === 'CONFIRMATION_REQUIRED' ? `400 CONFIRMATION_REQUIRED：必须同时确认风险框并精确输入 "${target.instance_id}"。` :
          code === 'LEASE_VERSION_CONFLICT' ? '409 LEASE_VERSION_CONFLICT：实例驻留世代已变更，已刷新账本，请关闭弹窗后重新核对。' :
            code === 'RECLAIM_FAILED_QUARANTINED' ? '503 RECLAIM_FAILED_QUARANTINED：子进程回收或封条校验未通过，问题显存已隔离冻结。' :
              `强制清场结果待确认（${code || result.status}），请刷新核验。`
      await gpuSchedulerPolling?.refresh()
      return
    }
    const action = result.data.result
    await Promise.all([gpuSchedulerPolling?.refresh(), polling?.refresh()])
    if (epoch !== scopeEpoch || disposed) return
    forceDraining.value = false
    closeForceDrain()
    toast.value = `已完成 ${action.instance_id} 管理员强制清场（boot_id=${action.boot_id} 保留不变，审计游标 #${action.ledger_event_seq}）。`
  } catch {
    if (epoch === scopeEpoch && !disposed) {
      forceDrainError.value = '连接异常，强制清场结果待确认；重试将复用原幂等键。'
    }
  } finally {
    if (epoch === scopeEpoch) forceDraining.value = false
  }
}

async function changeView(next: ResourceView) {
  if (next === currentView.value) return
  scrolls[currentView.value] = host?.getScroll?.() ?? 0
  currentView.value = next
  volumePolling?.setEnabled(next === 'storage')
  modelPolling?.setEnabled(next === 'storage')
  gpuSchedulerPolling?.setEnabled(next === 'tasks')
  await nextTick()
  host?.resize(Math.ceil(document.getElementById('app')!.getBoundingClientRect().height))
  cancelAnimationFrame(restoreFrame)
  restoreFrame = requestAnimationFrame(() => { if (!disposed && active) host?.restoreScroll?.(scrolls[next]) })
}
async function selectStorageSubtab(next: StorageSubtab) {
  if (storageSubtab.value === next) return
  storageSubtab.value = next
  await nextTick()
  host?.resize(Math.ceil(document.getElementById('app')!.getBoundingClientRect().height))
}
async function navigateStorage() { await sdk?.navigation?.request?.({ target: 'app', path: '/storage' }) }
async function refresh() {
  refreshing.value = true
  try {
    await Promise.all([
      polling?.refresh(),
      currentView.value === 'storage' ? volumePolling?.refresh() : undefined,
      currentView.value === 'storage' ? modelPolling?.refresh() : undefined,
      currentView.value === 'tasks' ? gpuSchedulerPolling?.refresh() : undefined,
    ])
  }
  finally { refreshing.value = false }
}
function publishCounts() {
  const detail = { workspaces: volumes.value?.length ?? null, tasks: dashboard.value ? workloads.value.length : null }
  window.dispatchEvent(new CustomEvent('studio:resources-counts', { detail }))
}

const modelsList = computed(() => modelsData.value?.models ?? [])
const modelWatermark = computed(() => modelsData.value?.watermark ?? null)
const modelPollDegraded = computed(() => Boolean(modelError.value) || modelFailures.value > 0 || modelState.value === 'error')
const modelCategoryOptions = computed(() => Array.from(new Set(modelsList.value.map(m => m.category))).sort())
const modelStatusOptions: { value: string; label: string }[] = [
  { value: 'ready', label: '已就绪 (ready)' },
  { value: 'downloading', label: '下载中 (downloading)' },
  { value: 'verifying_sha256', label: '核验中 (verifying_sha256)' },
  { value: 'queued', label: '单飞排队 (queued)' },
  { value: 'evicting', label: '清理中 (evicting)' },
  { value: 'failed', label: '下载中断 (failed)' },
  { value: 'not_installed', label: '未缓存 (not_installed)' },
]
const visibleModels = computed(() => modelsList.value.filter(m =>
  (modelCategory.value === 'all' || m.category === modelCategory.value) &&
  (modelStatusFilter.value === 'all' || m.status === modelStatusFilter.value) &&
  (modelOriginFilter.value === 'all' || m.origin_kind === modelOriginFilter.value) &&
  `${m.catalog_code} ${m.display_name} ${m.model_id} ${m.filename} ${m.relative_view_path} ${m.sha256} ${m.category}`
    .toLocaleLowerCase().includes(modelSearch.value.trim().toLocaleLowerCase())))
function modelProgressRatio(m: ModelCacheItem): number {
  if (m.status === 'ready' || m.status === 'verifying_sha256') return 100
  if (!valid(m.size_bytes) || m.size_bytes <= 0) return 0
  const done = Math.max(valid(m.bytes_downloaded) ? m.bytes_downloaded : 0, valid(m.part_bytes_on_disk) ? m.part_bytes_on_disk : 0)
  return Math.min(100, Math.max(0, (done / m.size_bytes) * 100))
}
function modelEtaText(eta: number | null): string {
  if (!valid(eta) || eta < 0) return '计算中'
  if (eta < 60) return `剩余 ${Math.ceil(eta)}s`
  return `剩余 ${Math.floor(eta / 60)}m ${Math.ceil(eta % 60)}s`
}
function modelGateContext() {
  return {
    active: Boolean(active && pageActive.value && !disposed),
    pollDegraded: modelPollDegraded.value,
    modelsReady: Boolean(modelsData.value),
    liveModels: modelsList.value,
  }
}
function canEvictModelReason(m: ModelCacheItem | null | undefined): string {
  return modelEvictBlockReason(m, modelGateContext())
}

const modelEvictIntents = createModelEvictIntents()
const lockedModelDialog = ref<HTMLDialogElement>(), lockedModelTarget = ref<ModelCacheItem | null>(null)
const evictModelDialog = ref<HTMLDialogElement>(), evictModelTarget = ref<ModelCacheItem | null>(null)
const evictModelError = ref(''), evictingModel = ref(false)
const evictModelBlock = computed(() => canEvictModelReason(evictModelTarget.value))

function openLockedModel(m: ModelCacheItem) {
  if (!active || disposed) return
  lockedModelTarget.value = { ...m, pin_scopes: [...m.pin_scopes] }
  lockedModelDialog.value?.showModal()
}
function closeLockedModel() {
  lockedModelDialog.value?.close()
  lockedModelTarget.value = null
}
function openEvictModel(m: ModelCacheItem) {
  if (!active || disposed) return
  if (m.active_pins + m.reserved_pins > 0) {
    openLockedModel(m)
    return
  }
  const block = canEvictModelReason(m)
  if (block) {
    toast.value = block
    return
  }
  evictModelTarget.value = { ...m, pin_scopes: [...m.pin_scopes] }
  evictModelError.value = ''
  evictModelDialog.value?.showModal()
}
function closeEvictModel() {
  if (evictingModel.value) return
  evictModelDialog.value?.close()
  evictModelTarget.value = null
  evictModelError.value = ''
}
async function submitEvictModel() {
  const target = evictModelTarget.value
  if (!target || !sdk || evictingModel.value) return
  const block = canEvictModelReason(target)
  if (block) {
    evictModelError.value = block
    return
  }
  const epoch = scopeEpoch
  let idempotencyKey: string
  try {
    idempotencyKey = modelEvictIntents.key(target)
  } catch {
    evictModelError.value = '当前模型状态不可清理，请刷新后重试。'
    return
  }
  evictingModel.value = true
  evictModelError.value = ''
  try {
    const result = await sdk.app.request({
      path: '/api/v1/models/evict',
      method: 'POST',
      body: { idempotency_key: idempotencyKey, sha256: target.sha256 },
    })
    if (epoch !== scopeEpoch || disposed) return
    if (result.status !== 200 || !isModelEvictSuccess(result.data, target.sha256)) {
      const payload = result.data as { code?: string; active_pins?: number; reserved_pins?: number } | null
      const code = payload?.code
      await modelPolling?.refresh()
      if (epoch !== scopeEpoch || disposed) return
      if (result.status === 409 || code === 'MODEL_IN_USE') {
        const refreshed = modelsList.value.find(m => m.sha256 === target.sha256)
        const lockedItem: ModelCacheItem = refreshed ?? {
          ...target,
          active_pins: typeof payload?.active_pins === 'number' ? payload.active_pins : Math.max(1, target.active_pins),
          reserved_pins: typeof payload?.reserved_pins === 'number' ? payload.reserved_pins : target.reserved_pins,
        }
        evictingModel.value = false
        closeEvictModel()
        openLockedModel(lockedItem)
        return
      }
      evictModelError.value = result.status === 403
        ? '403 FORBIDDEN：当前身份没有清理模型缓存的权限。'
        : `模型缓存清理结果待确认（${code || result.status}），已刷新台账；重试复用原幂等键。`
      return
    }
    await Promise.all([modelPolling?.refresh(), polling?.refresh()])
    if (epoch !== scopeEpoch || disposed) return
    evictingModel.value = false
    closeEvictModel()
    toast.value = `模型缓存 ${target.catalog_code} (${target.filename}) 已安全清理，登记元数据仍保留为 not_installed。`
  } catch {
    if (epoch === scopeEpoch && !disposed) {
      evictModelError.value = '连接异常，模型缓存清理结果待确认；重试将复用原幂等键。'
    }
  } finally {
    if (epoch === scopeEpoch) evictingModel.value = false
  }
}

const releaseDialog = ref<HTMLDialogElement>(), releaseTarget = ref<WorkspaceVolume | null>(null), releaseName = ref(''), releaseError = ref(''), releasing = ref(false)
const releaseIntents = createReleaseIntents()
const currentTarget = computed(() => volumes.value?.find(w => releaseTarget.value && workspaceKey(w) === workspaceKey(releaseTarget.value)))
const releaseBlock = computed(() => {
  if (!active || error.value || volumeError.value || !volumes.value) return '当前数据未确认，请恢复连接并刷新后重试'
  const current = currentTarget.value
  if (!current || current.pv_name !== releaseTarget.value?.pv_name) return '工作卷已变更，请刷新台账并重新确认'
  return releaseReason(current)
})
function openRelease(w: WorkspaceVolume) {
  if (releaseReason(w) || error.value || volumeError.value || !active) return
  releaseTarget.value = { ...w }; releaseName.value = ''; releaseError.value = ''
  releaseDialog.value?.showModal()
}
function closeRelease() {
  if (releasing.value) return
  releaseDialog.value?.close(); releaseTarget.value = null; releaseName.value = ''; releaseError.value = ''
}
async function submitRelease() {
  const target = currentTarget.value
  if (!target || !sdk || releasing.value || releaseBlock.value || releaseName.value !== target.pvc_name) return
  const epoch = scopeEpoch, idempotencyKey = releaseIntents.key(target, releaseName.value)
  releasing.value = true; releaseError.value = ''
  try {
    const result = await sdk.app.request({ path: '/api/v1/workspaces/release', method: 'POST',
      body: { namespace: target.namespace, pvc_name: target.pvc_name, idempotency_key: idempotencyKey } })
    if (epoch !== scopeEpoch || disposed) return
    if (result.status !== 200 || !isReleased(result.data, target)) {
      const code = (result.data as { code?: string } | null)?.code
      releaseError.value = code === 'INSTANCE_NOT_DESTROYED' ? '关联实例尚未销毁，请先在应用控制台销毁。' :
        code === 'WORKSPACE_IN_USE' ? '工作卷正在被挂载，无法释放。' : code === 'WORKSPACE_BINDING_CONFLICT' ? '绑定信息冲突，请刷新并核对工作卷。' :
          result.status === 403 ? '当前身份没有释放权限。' : '释放结果待确认，请刷新台账核验；重试将复用原幂等键。'
      await volumePolling?.refresh()
      return
    }
    await Promise.all([volumePolling?.refresh(), polling?.refresh()])
    if (epoch !== scopeEpoch || disposed) return
    if (volumeError.value || volumes.value?.some(w => workspaceKey(w) === workspaceKey(target))) {
      releaseError.value = '服务端已确认释放，台账更新待确认。请刷新核验。'
      return
    }
    releasing.value = false; closeRelease()
    toast.value = `PVC ${target.pvc_name} 已释放，底层 PV 回到可分配状态。`
  } catch {
    if (epoch === scopeEpoch && !disposed) releaseError.value = '连接中断，释放结果待确认。请刷新核验；重试复用原幂等键。'
  } finally { if (epoch === scopeEpoch) releasing.value = false }
}
function resetScope() {
  const next = sdk ? contextScope(sdk) : null
  if (next !== null && next === scope) return
  scope = next
  scopeEpoch++; releasing.value = false; closeRelease(); releaseIntents.clear(); toast.value = ''
  evictingModel.value = false; closeLockedModel(); closeEvictModel(); modelEvictIntents.clear()
  forceDraining.value = false; closeBlockedEvict(); closeForceDrain(); residencyIntents.clear()
  evictingIds.value.clear(); reclaimingAll.value = false; expandedAuditEvents.value = false
  clearVolumes(); clearModels(); clearTasks(); expandedGPU.value.clear(); expandedVolume.value.clear(); expandedModel.value.clear(); expandedTask.value.clear(); expandedHost.value = false
  for (const key of Object.keys(scrolls) as ResourceView[]) scrolls[key] = 0
}
function cleanup() {
  if (disposed) return
  disposed = true; pageActive.value = false; scopeEpoch++; polling?.dispose(); volumePolling?.dispose(); modelPolling?.dispose(); gpuSchedulerPolling?.dispose(); observer?.disconnect()
  if (sampleClockTimer !== undefined) { clearInterval(sampleClockTimer); sampleClockTimer = undefined }
  cancelAnimationFrame(restoreFrame); subscriptions.splice(0).forEach(stop => stop())
  releaseDialog.value?.close(); lockedModelDialog.value?.close(); evictModelDialog.value?.close(); blockedEvictDialog.value?.close(); forceDrainDialog.value?.close()
  releaseIntents.clear(); modelEvictIntents.clear(); residencyIntents.clear()
}
onMounted(() => {
  const props = hostProps()
  if (!props) { state.value = 'error'; error.value = '请从 Studio 打开资源管理。'; return }
  sdk = props.sdk; host = props.host
  scope = contextScope(sdk)
  document.body.dataset.theme = sdk.theme.get()
  subscriptions.push(sdk.theme.subscribe(theme => { document.body.dataset.theme = theme }))
  currentView.value = parseResourceView(sdk.navigation?.get().path ?? window.location.search)
  if (sdk.navigation) subscriptions.push(sdk.navigation.subscribe(route => { void changeView(parseResourceView(route.path)) }))
  subscriptions.push(sdk.context.subscribe(resetScope))
  subscriptions.push(sdk.lifecycle.subscribe(event => {
    active = event === 'active'
    pageActive.value = active
    if (event === 'dispose') cleanup()
  }))
  sampleClockTimer = setInterval(() => { sampleClockMs.value = Date.now() }, 5000)
  polling = createPolling(sdk, result => {
    state.value = result.state
    dashboard.value = result.data
    error.value = result.message
    pollFailures.value = result.failures ?? 0
    pollBackoffMs.value = result.backoffMs ?? 0
    pollTripped.value = !!result.tripped
    publishCounts()
  })
  volumePolling = createScopedPolling(sdk, '/api/v1/workspaces', isWorkspacesResponse, result => {
    volumeState.value = result.state
    volumes.value = result.data?.workspaces ?? null
    volumeError.value = result.message
    volumeFailures.value = result.failures ?? 0
    volumeBackoffMs.value = result.backoffMs ?? 0
    volumeTripped.value = !!result.tripped
    publishCounts()
  }, undefined, currentView.value === 'storage')
  modelPolling = createScopedPolling(sdk, '/api/v1/models', isModelsResponse, result => {
    modelState.value = result.state
    modelsData.value = result.data
    modelError.value = result.message
    modelFailures.value = result.failures ?? 0
    modelBackoffMs.value = result.backoffMs ?? 0
    modelTripped.value = !!result.tripped
  }, undefined, currentView.value === 'storage')
  gpuSchedulerPolling = createScopedPolling(sdk, '/api/v1/gpu-scheduler', isGPUSchedulerResponse, result => {
    sampleClockMs.value = Date.now()
    gpuSchedulerState.value = result.state
    gpuScheduler.value = result.data
    gpuSchedulerError.value = result.message
    gpuSchedulerFailures.value = result.failures ?? 0
    gpuSchedulerBackoffMs.value = result.backoffMs ?? 0
    gpuSchedulerTripped.value = !!result.tripped
  }, undefined, currentView.value === 'tasks')
  const root = document.getElementById('app')!
  observer = new ResizeObserver(() => host?.resize(Math.ceil(root.getBoundingClientRect().height)))
  observer.observe(root); host.ready(); void refresh()
})
onBeforeUnmount(cleanup)
</script>

<template>
  <main class="resource-page" data-embedded="true">
    <header class="page-heading"><h1>{{ meta[currentView].title }}</h1><p>{{ meta[currentView].description }}</p></header>
    <div class="telemetry-toolbar"><span class="sample-state tabular-nums"><i class="status-dot" :class="{ warning: error }"></i>{{ error ? '更新失败 · 上次观测' : '观测时间' }} · {{ sampleTime(dashboard?.observed_at) }}</span><button class="button" :disabled="refreshing" @click="refresh">{{ refreshing ? '刷新中…' : '刷新' }}</button></div>
    <p v-if="toast" class="notice" role="status">{{ toast }}</p>
    <div v-if="error" class="notice backoff-banner telemetry-degraded-banner" :class="{ tripped: pollTripped }" role="alert">
      <div class="backoff-meta">
        <span>{{ error }}</span>
        <span v-if="pollFailures > 0" class="backoff-pill">{{ pollTripped ? `Circuit Breaker: OPEN (${pollFailures}x) · Backoff ${pollBackoffMs}ms` : `Attempt ${pollFailures}/3 · Backoff ${pollBackoffMs}ms (±15% Jitter)` }}</span>
      </div>
      <button class="link-button" @click="refresh">重试</button>
    </div>
    <div v-if="state === 'loading' && !dashboard" class="kpi-grid" aria-label="正在读取资源观测" role="status"><div v-for="i in 4" :key="i" class="skeleton-card"><span class="skel-bar"></span><span class="skel-bar"></span></div></div>
    <div v-else-if="!dashboard" class="state-box panel"><h2>资源观测暂不可用</h2><p>请确认 Station 连接与当前查看权限。</p><button class="button" @click="refresh">重新读取</button></div>
    <template v-if="dashboard && currentView === 'monitor'">
      <p v-if="hardwareNotice" class="notice" role="status">{{ hardwareNotice }}</p>
      <div class="kpi-grid">
        <article class="kpi summary-card panel"><div class="kpi-label">GPU 设备<ResourceIcon name="gpu" /></div><div class="kpi-value summary-value tabular-nums">{{ numeric(summary.total_gpus, 0) }}<small>张</small></div><div class="kpi-foot">{{ dashboard.gpus === null ? '设备采样不可用，覆盖范围未提供' : `已监测 ${gpus.length} 张` }}</div><div class="kpi-foot tabular-nums">显存 {{ scaled(summary.used_vram_mb, 1024) }} / {{ scaled(summary.total_vram_mb, 1024) }} GiB</div></article>
        <article class="kpi summary-card panel"><div class="kpi-label">CPU 使用率<ResourceIcon name="cpu" /></div><div class="kpi-value summary-value tabular-nums">{{ numeric(summary.host_cpu_utilization) }}<small>%</small></div><div class="kpi-foot tabular-nums">{{ numeric(summary.host_cpu_cores, 0) }} vCPU <span>1 分钟负载 {{ numeric(summary.host_cpu_load1, 2) }}</span></div></article>
        <article class="kpi summary-card panel"><div class="kpi-label">系统内存<ResourceIcon name="memory" /></div><div class="kpi-value summary-value tabular-nums">{{ numeric(summary.host_mem_used_gb) }}<small>/ {{ numeric(summary.host_mem_total_gb) }} GiB</small></div><div class="kpi-foot tabular-nums">使用率 {{ numeric(summary.host_mem_used_percent) }}%</div><div class="meter blue"><span :style="meter(summary.host_mem_used_percent)"></span></div></article>
        <article class="kpi summary-card panel"><div class="kpi-label">存储使用量<ResourceIcon name="disk" /></div><div class="kpi-value summary-value storage-value">查看挂载点</div><div class="kpi-foot">汇总去重范围未提供</div><button class="link-button" @click="navigateStorage">查看存储管理 →</button></article>
      </div>
      <div class="section-head"><h2>GPU 设备</h2><span>{{ dashboard.gpus === null ? '采样不可用' : `已监测 ${gpus.length} 张` }}</span></div>
      <div v-if="!gpus.length" class="state-box panel"><h2>{{ dashboard.gpus === null ? 'GPU 观测暂不可用' : '尚未发现 GPU 设备' }}</h2><p>以 Station 返回的设备与采样状态为准。</p></div>
      <div class="resource-grid">
        <article v-for="g in gpus" :key="gpuKey(g)" class="resource-card gpu-card panel">
          <div class="card-heading"><div class="card-name"><div class="device-icon"><ResourceIcon name="gpu" /></div><div><h3>GPU {{ g.index }} · {{ g.model_name || g.device }}</h3><p class="gpu-util tabular-nums">GPU 利用率 {{ numeric(g.utilization) }}%</p></div></div><span class="badge" :class="g.quality === 'fresh' ? 'good' : 'warning'"><i></i>{{ quality(g.quality) }}</span></div>
          <div class="metric-line"><span>显存占用</span><strong class="metric-val tabular-nums">{{ scaled(g.used_vram_mb, 1024) }} <small>/ {{ scaled(g.total_vram_mb, 1024) }} GiB</small></strong></div>
          <div class="meter"><span :style="meter(ratio(g.used_vram_mb, g.total_vram_mb))"></span></div><div class="metric-caption bar-label tabular-nums"><span>显存容量使用率 {{ numeric(ratio(g.used_vram_mb, g.total_vram_mb)) }}%</span><span>空闲 {{ scaled(g.free_vram_mb, 1024) }} GiB</span></div>
          <div class="metric-split two"><div><small>温度</small><b class="metric-val tabular-nums">{{ numeric(g.temperature_c) }} °C</b></div><div><small>功耗</small><b class="metric-val tabular-nums">{{ numeric(g.power_watts) }} W</b></div></div>
          <div class="card-bottom"><span>采样 {{ sampleTime(g.sampled_at) }}</span><button class="link-button" :aria-expanded="expandedGPU.has(gpuKey(g))" @click="toggle(expandedGPU, gpuKey(g))">{{ expandedGPU.has(gpuKey(g)) ? '收起详情' : '展开详情' }} ⌄</button></div>
          <div v-if="expandedGPU.has(gpuKey(g))" class="inline-detail"><p>设备 UUID：{{ g.uuid || '未提供' }}</p><p>设备标识：{{ g.device || '未提供' }}</p><p>节点 / 硬件错误信息：未提供</p><p>采样质量仅表示观测状态；空闲显存不表示任务准入。</p></div>
        </article>
      </div>
      <div class="section-head"><h2>主机资源</h2><span>{{ quality(node?.quality) }} · {{ sampleTime(node?.sampled_at) }}</span></div>
      <div class="resource-grid">
        <article class="resource-card node-card panel"><div class="card-heading"><div class="card-name"><div class="device-icon"><ResourceIcon name="cpu" /></div><h3>CPU</h3></div><span class="badge tabular-nums">{{ numeric(node?.cpu_cores, 0) }} vCPU</span></div><div class="metric-line"><span>CPU 使用率</span><strong class="metric-val tabular-nums">{{ numeric(node?.cpu_utilization_percent) }}<small>%</small></strong></div><div class="meter"><span :style="meter(node?.cpu_utilization_percent)"></span></div><div class="metric-split"><div v-for="(value, i) in [node?.cpu_load1, node?.cpu_load5, node?.cpu_load15]" :key="i"><small>系统负载 · {{ [1,5,15][i] }} 分钟</small><b class="metric-val tabular-nums">{{ numeric(value, 2) }}</b></div></div><div class="card-bottom"><span>CPU 百分比与系统负载为不同口径</span><button class="link-button" :aria-expanded="expandedHost" @click="expandedHost = !expandedHost">节点明细 ⌄</button></div><div v-if="expandedHost" class="inline-detail">节点名称：未提供 · 采样 {{ sampleTime(node?.sampled_at) }}</div></article>
        <article class="resource-card node-card panel"><div class="card-heading"><div class="card-name"><div class="device-icon"><ResourceIcon name="memory" /></div><h3>系统内存</h3></div><span class="badge tabular-nums">{{ numeric(node?.mem_used_percent) }}%</span></div><div class="metric-line"><span>已用 / 总量</span><strong class="metric-val tabular-nums">{{ scaled(node?.mem_used_bytes, 1024 ** 3) }}<small>/ {{ scaled(node?.mem_total_bytes, 1024 ** 3) }} GiB</small></strong></div><div class="meter blue"><span :style="meter(node?.mem_used_percent)"></span></div><div class="metric-split two"><div><small>可用</small><b class="metric-val tabular-nums">{{ formatResource(node?.mem_available_bytes, 'bytes') }}</b></div><div><small>文件缓存</small><b class="metric-val tabular-nums">{{ formatResource(node?.mem_cached_bytes, 'bytes') }}</b></div></div><div class="card-bottom"><span>交换区：未提供</span><button class="link-button" :aria-expanded="expandedHost" @click="expandedHost = !expandedHost">节点明细 ⌄</button></div><div v-if="expandedHost" class="inline-detail"><p>缓冲区 {{ formatResource(node?.mem_buffers_bytes, 'bytes') }}</p><p>使用量遵循服务端统计口径，缓存与缓冲区不重复相加。</p></div></article>
      </div>
    </template>
    <template v-if="currentView === 'storage'">
      <p v-if="hardwareNotice" class="notice">{{ hardwareNotice }}</p>
      <div class="section-head"><h2>磁盘容量</h2><span>物理容量与工作卷预留容量分别统计</span></div>
      <div v-if="!disks.length" class="state-box panel"><h2>{{ node?.storage_disks?.length === 0 ? '暂无挂载点' : '磁盘观测暂不可用' }}</h2><p>接口未返回容量时不展示示例磁盘。</p></div>
      <div class="resource-grid"><article v-for="d in disks" :key="d.device + d.mountpoint" class="resource-card storage-card panel"><div class="card-heading"><div class="card-name"><div class="device-icon"><ResourceIcon name="disk" /></div><div><h3>{{ d.mountpoint === '/' ? '系统盘' : d.mountpoint === '/data' ? '数据盘' : '挂载点' }} · {{ d.mountpoint }}</h3><p>{{ d.device || '设备未提供' }} · {{ d.fstype || '文件系统未提供' }}</p></div></div><span class="badge tabular-nums">{{ numeric(d.used_percent) }}%</span></div><div class="metric-line"><span>已用 / 总量</span><strong class="metric-val tabular-nums">{{ scaled(d.used_bytes, 1024 ** 3) }}<small>GiB / {{ scaled(d.total_bytes, 1024 ** 4, 2) }} TiB</small></strong></div><div class="meter"><span :style="meter(d.used_percent)"></span></div><div class="metric-caption bar-label tabular-nums"><span>可用 {{ formatResource(d.avail_bytes, 'bytes') }}</span><span>容量使用率 {{ numeric(d.used_percent) }}%</span></div><div class="card-bottom">采样 {{ sampleTime(node?.sampled_at) }} · 节点未提供</div></article></div>

      <div class="storage-subbar">
        <div class="storage-tabs" role="tablist" aria-label="存储管理子视图">
          <button type="button" class="storage-tab" role="tab" :aria-selected="storageSubtab === 'models'" @click="selectStorageSubtab('models')">
            模型缓存库（CAS 只读共享）
            <span class="tab-count">{{ modelsData ? modelsData.total_count : '—' }}</span>
          </button>
          <button type="button" class="storage-tab" role="tab" :aria-selected="storageSubtab === 'workspaces'" @click="selectStorageSubtab('workspaces')">
            应用工作卷池
            <span class="tab-count">{{ volumes ? volumes.length : '—' }}</span>
          </button>
        </div>
        <span class="filter-status">{{ storageSubtab === 'models' ? '容器统一只读挂载 /models:ro · 宿主机物理路径已隔离脱敏' : `${volumes?.length ?? '—'} 个工作卷 · 待释放 ${volumes ? volumeCounts.retained : '—'} 个` }}</span>
      </div>

      <template v-if="storageSubtab === 'models'">
        <div v-if="modelWatermark" class="watermark-banner" aria-label="共享模型缓存水位护栏">
          <div class="watermark-main">
            <div class="device-icon"><ResourceIcon name="disk" /></div>
            <div class="watermark-copy">
              <h3>共享模型缓存池（只读视图 {{ modelsData?.mount_path || '/models' }}:ro）</h3>
              <p>已就绪缓存 {{ formatModelBytes(modelWatermark.cached_ready_bytes) }} · 拉取预留 {{ formatModelBytes(modelWatermark.unmaterialized_reserved_bytes) }} · 文件系统可用 {{ formatModelBytes(modelWatermark.filesystem_free_bytes) }} / {{ formatModelBytes(modelWatermark.filesystem_total_bytes) }}</p>
            </div>
          </div>
          <div class="watermark-pills">
            <span class="cursor-pill">安全水位护栏 <b>{{ formatModelBytes(modelWatermark.reserved_headroom_bytes) }}</b></span>
            <span class="cursor-pill">自动拉取单模上限 <b>{{ formatModelBytes(modelWatermark.max_auto_pull_model_bytes) }}</b></span>
            <span class="badge" :class="modelWatermark.headroom_compliant === false ? 'warning' : 'good'">
              <i></i>{{ modelWatermark.headroom_compliant === false ? '低于安全余量 · 触发 LRU 淘汰评估' : '安全余量达标' }}
            </span>
          </div>
        </div>
        <p v-if="modelsData?.truncated" class="notice" role="status">当前目录分页已达安全上限，仅展示前 {{ modelsList.length }} / {{ modelsData.total_count }} 项模型记录。</p>

        <div class="filter-toolbar">
          <label class="search"><input v-model="modelSearch" aria-label="搜索模型缓存" placeholder="搜索 MDL-* 编号、模型名称、文件名或 SHA-256…"></label>
          <select v-model="modelCategory" class="filter-select" aria-label="模型分类筛选">
            <option value="all">全部模型分类</option>
            <option v-for="cat in modelCategoryOptions" :key="cat" :value="cat">{{ cat }}</option>
          </select>
          <select v-model="modelStatusFilter" class="filter-select" aria-label="模型七态状态筛选">
            <option value="all">全部七态状态</option>
            <option v-for="opt in modelStatusOptions" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
          </select>
          <select v-model="modelOriginFilter" class="filter-select" aria-label="模型目录来源筛选">
            <option value="all">全部来源</option>
            <option value="public">公共目录 (Center 同构)</option>
            <option value="local">本站登记 (MDL-*-LOCAL-*)</option>
          </select>
          <span class="filter-status filter-tail">显示 {{ visibleModels.length }} / {{ modelsData?.total_count ?? '—' }} 项</span>
        </div>

        <div v-if="modelError" class="notice backoff-banner telemetry-degraded-banner" :class="{ tripped: modelTripped }" role="alert">
          <div class="backoff-meta">
            <span>模型缓存台账：{{ modelError }}</span>
            <span v-if="modelFailures > 0" class="backoff-pill">{{ modelTripped ? `Circuit Breaker: OPEN (${modelFailures}x) · Backoff ${modelBackoffMs}ms` : `Attempt ${modelFailures}/3 · Backoff ${modelBackoffMs}ms (±15% Jitter)` }}</span>
          </div>
          <button class="link-button" @click="refresh">重试</button>
        </div>

        <div v-if="modelState === 'loading' && !modelsData" class="state-box panel" role="status">正在读取共享模型缓存库与七态流转进度…</div>
        <div v-else-if="!modelsData" class="state-box panel"><h2>模型缓存台账暂不可用</h2><p>磁盘观测与工作卷池独立显示，请确认 Station 模型池权限后重试。</p></div>
        <div v-else-if="!visibleModels.length" class="state-box panel">
          <h2>{{ modelsList.length ? '没有符合筛选条件的模型目录条目' : '尚未登记共享模型缓存' }}</h2>
          <p>{{ modelsList.length ? '试试其他关键词或状态筛选。' : '通过智能体调用 model.catalog_search 与 model.ensure 后，单飞拉取进度将实时同步至此。' }}</p>
          <button v-if="modelsList.length" class="button" @click="clearModels">清除筛选</button>
        </div>
        <div v-else class="table-panel panel">
          <table class="model-table" aria-label="ComfyUI 七态模型缓存库台账">
            <thead>
              <tr>
                <th>编号 / 模型标识与来源</th>
                <th>分类</th>
                <th>字节大小 / 实时进度（零宿主机路径）</th>
                <th>七态生命周期</th>
                <th>引用锁</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              <template v-for="m in visibleModels" :key="m.sha256">
                <tr class="task-row">
                  <td data-label="模型编号">
                    <div>
                      <button class="link-button cell-main" :aria-expanded="expandedModel.has(m.sha256)" @click="toggle(expandedModel, m.sha256)">{{ m.catalog_code }}</button>
                      <span class="source-tag" :class="{ local: m.origin_kind === 'local' }">{{ m.origin_kind === 'local' ? '本站登记' : '公共目录' }}</span>
                    </div>
                    <span class="cell-sub">{{ m.model_id }} · {{ m.display_name || m.filename }}</span>
                  </td>
                  <td data-label="分类">
                    <span class="cat-code">{{ m.category }}</span>
                  </td>
                  <td data-label="进度">
                    <div v-if="m.status === 'downloading'" class="progress-stack">
                      <div><strong>{{ formatModelBytes(m.bytes_downloaded) }}</strong> / {{ formatModelBytes(m.size_bytes) }} ({{ numeric(modelProgressRatio(m), 1) }}%)</div>
                      <div class="meter blue"><span :style="meter(modelProgressRatio(m))"></span></div>
                      <div class="progress-meta">
                        <span>{{ formatSpeedMiBps(m.speed_bps) }}</span>
                        <span>{{ modelEtaText(m.eta_seconds) }}</span>
                      </div>
                    </div>
                    <div v-else-if="m.status === 'verifying_sha256'" class="progress-stack">
                      <div><strong>{{ formatModelBytes(m.size_bytes) }}</strong> · 全量字节已落盘</div>
                      <div class="meter blue"><span style="width:100%"></span></div>
                      <div class="progress-meta"><span>重算 SHA-256 与校验 .safetensors 头…</span><span>fence #{{ m.download_fence }}</span></div>
                    </div>
                    <div v-else-if="m.status === 'failed'">
                      <div><strong>{{ formatModelBytes(m.part_bytes_on_disk || m.bytes_downloaded) }}</strong> / {{ formatModelBytes(m.size_bytes) }}</div>
                      <span class="cell-sub" style="color:var(--danger)">{{ m.error_code || 'DOWNLOAD_INTERRUPTED' }}</span>
                    </div>
                    <div v-else-if="m.status === 'ready'">
                      <div><strong>{{ formatModelBytes(m.size_bytes) }}</strong> · SHA-256 已核验</div>
                      <span class="cell-sub mono-code">{{ m.relative_view_path }}</span>
                    </div>
                    <div v-else>
                      <div><strong>{{ formatModelBytes(m.size_bytes) }}</strong></div>
                      <span class="cell-sub">{{ modelPinSummary(m) }}</span>
                    </div>
                  </td>
                  <td data-label="生命周期">
                    <span class="badge" :class="modelStatusBadge(m.status).tone">{{ modelStatusBadge(m.status).label }}</span>
                  </td>
                  <td data-label="引用锁">
                    <template v-if="m.active_pins + m.reserved_pins > 0">
                      <strong>{{ m.active_pins + m.reserved_pins }} 个锁</strong>
                      <span class="cell-sub">active:{{ m.active_pins }} / res:{{ m.reserved_pins }}</span>
                    </template>
                    <span v-else style="color:var(--muted)">0 引用</span>
                  </td>
                  <td data-label="操作">
                    <button v-if="m.status === 'downloading' || m.status === 'verifying_sha256' || m.status === 'queued' || m.status === 'evicting'" type="button" class="link-button" :aria-expanded="expandedModel.has(m.sha256)" @click="toggle(expandedModel, m.sha256)">{{ expandedModel.has(m.sha256) ? '收起详情' : '查看详情' }} ⌄</button>
                    <button v-else-if="(m.status === 'ready' || m.status === 'failed') && m.active_pins + m.reserved_pins > 0" type="button" class="table-action" :title="`存在 ${m.active_pins + m.reserved_pins} 个活跃/预留引用锁，点击查看 409 MODEL_IN_USE 保护`" @click="openLockedModel(m)">锁止禁删 ⓘ</button>
                    <button v-else-if="m.status === 'ready'" type="button" class="table-action release" :disabled="Boolean(canEvictModelReason(m))" :title="canEvictModelReason(m) || '清理本地 CAS 模型缓存'" @click="openEvictModel(m)">清理缓存</button>
                    <button v-else-if="m.status === 'failed'" type="button" class="table-action release" :disabled="Boolean(canEvictModelReason(m))" :title="canEvictModelReason(m) || '清理断点续传残留分片并重置状态'" @click="openEvictModel(m)">清理残留</button>
                    <span v-else class="disabled-action" tabindex="0" title="未落盘条目由智能体调用 model.ensure 按需受控拉取">无本地缓存块<span class="cell-sub">由 model.ensure 拉取</span></span>
                  </td>
                </tr>
                <tr v-if="expandedModel.has(m.sha256)" class="row-detail">
                  <td colspan="6">
                    <div class="inline-detail">
                      <p><strong>受控只读相对视图（零宿主机隐私路径）</strong>：<span class="mono-code">{{ m.relative_view_path }}</span>（容器挂载于 <span class="mono-code">/models/{{ m.relative_view_path }}</span>）</p>
                      <p><strong>完整 SHA-256 摘要</strong>：<span class="mono-code">{{ m.sha256 }}</span> · 单飞围栏 <span class="mono-code">download_fence=#{{ m.download_fence }}</span></p>
                      <p><strong>合规与显存画像</strong>：开源协议 <strong>{{ m.license || '未标注' }}</strong> · 付费解锁标记 <strong>paid_access={{ m.paid_access }}</strong> · 基准显存 <strong>{{ m.vram_footprint_mib }} MiB</strong></p>
                      <p><strong>引用锁明细（station.model_ref_pins）</strong>：{{ modelPinSummary(m) }}</p>
                      <p v-if="m.error_message" style="color:var(--danger)">最近诊断：{{ m.error_message }}</p>
                    </div>
                  </td>
                </tr>
              </template>
            </tbody>
          </table>
          <div class="table-caption">显示 {{ visibleModels.length }} / {{ modelsData.total_count }} 条标准模型目录项 · 多实例共享唯一物理 inode 与 Linux Page Cache</div>
        </div>
        <p class="table-note">说明：下载进度实时采样字节计数与吞吐速率（不提供伪造的暂停按钮；网络中断后支持 ETag/Range 安全断点续传）。存在活跃任务或已发布 API 引用锁（station.model_ref_pins）时服务端硬拦截清理并返回 409 MODEL_IN_USE。</p>
      </template>

      <template v-else>
        <div class="section-head"><h2>应用工作卷</h2><span>{{ volumes?.length ?? '—' }} 个 · 待释放 {{ volumes ? volumeCounts.retained : '—' }} 个</span></div>
        <div class="filter-toolbar"><label class="search"><input v-model="volumeSearch" aria-label="搜索工作卷" placeholder="搜索 PVC、PV、应用或实例…"></label><div class="chips"><button v-for="key in ['all','in_use','bound_stopped','retained']" :key="key" class="chip" :aria-pressed="volumeFilter === key" @click="volumeFilter = key">{{ key === 'all' ? '全部' : volumeLabels[key] }} {{ volumes ? volumeCounts[key] : '—' }}</button></div><span class="filter-status filter-tail">显示 {{ visibleVolumes.length }} / {{ volumes?.length ?? '—' }} 个</span></div>
        <div v-if="volumeError" class="notice backoff-banner telemetry-degraded-banner" :class="{ tripped: volumeTripped }" role="alert">
          <div class="backoff-meta">
            <span>{{ volumeError }}</span>
            <span v-if="volumeFailures > 0" class="backoff-pill">{{ volumeTripped ? `Circuit Breaker: OPEN (${volumeFailures}x) · Backoff ${volumeBackoffMs}ms` : `Attempt ${volumeFailures}/3 · Backoff ${volumeBackoffMs}ms (±15% Jitter)` }}</span>
          </div>
          <button class="link-button" @click="refresh">重试</button>
        </div>
        <div v-if="volumeState === 'loading' && !volumes" class="state-box panel" role="status">正在读取应用工作卷…</div>
        <div v-else-if="!volumes" class="state-box panel"><h2>工作卷台账暂不可用</h2><p>磁盘观测独立显示，请确认台账查看权限后重试。</p></div>
        <div v-else-if="!visibleVolumes.length" class="state-box panel"><h2>{{ volumes.length ? '没有符合条件的记录' : '暂无应用工作卷' }}</h2><p>{{ volumes.length ? '试试其他关键词或状态。' : '创建实例后，工作卷按现行卷池策略分配。' }}</p><button v-if="volumes.length" class="button" @click="clearVolumes">清除筛选</button></div>
        <div v-else class="table-panel panel"><table class="volume-table"><thead><tr><th>PVC</th><th>PV</th><th>所属应用 / 卷池</th><th>关联实例</th><th>状态</th><th>预留容量</th><th>操作</th></tr></thead><tbody><template v-for="w in visibleVolumes" :key="workspaceKey(w)"><tr class="task-row"><td data-label="PVC"><button class="link-button cell-main" :aria-expanded="expandedVolume.has(workspaceKey(w))" @click="toggle(expandedVolume, workspaceKey(w))">{{ w.pvc_name }}</button><span class="cell-sub">{{ w.namespace }}</span></td><td data-label="PV">{{ w.pv_name || '未提供' }}</td><td data-label="所属应用 / 卷池"><span class="cell-main">{{ w.app_display_name || w.app_id || '未提供' }}</span><span class="cell-sub">{{ w.pool_name || '未提供' }}</span></td><td data-label="关联实例">{{ w.instance_name || (w.state === 'retained' ? '历史实例（已销毁）' : w.instance_id || '未提供') }}</td><td data-label="状态"><span class="badge" :class="w.state === 'in_use' ? 'good' : w.state === 'retained' ? 'warning' : ''">{{ volumeLabels[w.state] || '未知状态' }}</span></td><td data-label="预留容量" class="number num tabular-nums">{{ formatResource(w.storage_bytes, 'bytes') }}</td><td data-label="操作"><button v-if="!releaseReason(w) && !error && !volumeError" class="table-action release" @click="openRelease(w)">释放工作卷</button><span v-else class="disabled-action" tabindex="0" :title="releaseReason(w) || '当前数据未确认，请刷新'">{{ releaseReason(w) === '无释放权限' ? '无释放权限' : '释放工作卷（不可用）' }}<span class="cell-sub">{{ releaseReason(w) || '当前数据未确认' }}</span></span></td></tr><tr v-if="expandedVolume.has(workspaceKey(w))" class="row-detail"><td colspan="7"><div class="inline-detail"><p>PVC：{{ w.namespace }}/{{ w.pvc_name }} · PV：{{ w.pv_name }}</p><p>卷池 {{ w.pool_name }} · 存储类 {{ w.storage_class || '未提供' }} · 访问模式 {{ w.access_mode || '未提供' }}</p><p>节点 {{ w.node_name || '未提供' }} · 实例 {{ w.instance_id || '未提供' }}</p><p>绑定 {{ sampleTime(w.bound_at) }} · 保留 {{ sampleTime(w.retained_at) }}</p></div></td></tr></template></tbody></table></div>
        <p class="table-note">预留容量是工作卷声明容量，不代表物理磁盘实际占用；销毁实例不会自动释放工作卷。</p>
      </template>
    </template>
    <template v-if="dashboard && currentView === 'tasks'">
      <div v-if="schedulerSnapshot" class="kpi-grid">
        <article class="kpi summary-card panel">
          <div class="kpi-label">承诺账本占用<ResourceIcon name="gpu" /></div>
          <div class="kpi-value summary-value tabular-nums">{{ scaled(totalCommittedMiB, 1024) }}<small>/ {{ formatGiBFromMiB(totalSchedulerCapacityMiB) }}</small></div>
          <div class="kpi-foot tabular-nums">最高账本游标 #{{ maxLedgerSeq }}</div>
          <div class="meter"><span :style="meter(ratio(totalCommittedMiB, totalSchedulerCapacityMiB))"></span></div>
        </article>
        <article class="kpi summary-card panel">
          <div class="kpi-label">S2 可回收缓存<ResourceIcon name="disk" /></div>
          <div class="kpi-value summary-value tabular-nums">{{ scaled(totalEvictableMiB, 1024) }}<small>GiB</small></div>
          <div class="kpi-foot tabular-nums">{{ evictableResidencies.length }} 个空闲热缓存实例可零损卸载</div>
        </article>
        <article class="kpi summary-card panel">
          <div class="kpi-label">排队与 K8s 名额<ResourceIcon name="cpu" /></div>
          <div class="kpi-value summary-value tabular-nums">{{ queuedLeases.length }}<small>项排队</small></div>
          <div class="kpi-foot tabular-nums">K8s 共享名额 {{ totalK8sSlotsUsed }} / {{ schedulerSnapshot.app_gpu_quota }}</div>
          <div class="meter blue"><span :style="meter(ratio(totalK8sSlotsUsed, schedulerSnapshot.app_gpu_quota))"></span></div>
        </article>
        <article class="kpi summary-card panel">
          <div class="kpi-label">容器工作负载<ResourceIcon name="memory" /></div>
          <div class="kpi-value summary-value tabular-nums">{{ taskCounts.running }}<small>/ {{ workloads.length }} 就绪</small></div>
          <div class="kpi-foot tabular-nums">{{ taskCounts.waiting }} 等待就绪 · {{ taskCounts.error }} 异常</div>
        </article>
      </div>
      <div v-else class="kpi-grid"><article class="kpi summary-card panel"><div class="kpi-label">工作负载</div><div class="kpi-value summary-value tabular-nums">{{ workloads.length }}<small>个</small></div><div class="kpi-foot">当前运行中的容器工作负载</div></article><article v-for="key in (['running','waiting','error'] as const)" :key="key" class="kpi summary-card panel"><div class="kpi-label">{{ groups[key] }}</div><div class="kpi-value summary-value tabular-nums">{{ taskCounts[key] }}<small>个</small></div><div class="kpi-foot">按服务端状态与容器就绪情况</div></article><article v-if="taskCounts.other" class="kpi summary-card panel"><div class="kpi-label">其他状态</div><div class="kpi-value summary-value tabular-nums">{{ taskCounts.other }}<small>个</small></div><div class="kpi-foot">已结束、未知或其他状态</div></article></div>

      <div class="section-head">
        <h2>物理 GPU 四象限双层账本</h2>
        <span>严格区分「账本未承诺额度」「当前可授予额度 G_now」与「回收潜力 V_evictable」</span>
      </div>
      <div v-if="gpuSchedulerError" class="notice backoff-banner telemetry-degraded-banner" :class="{ tripped: gpuSchedulerTripped }" role="alert">
        <div class="backoff-meta">
          <span>GPU 调度账本：{{ gpuSchedulerError }}</span>
          <span v-if="gpuSchedulerFailures > 0" class="backoff-pill">{{ gpuSchedulerTripped ? `Circuit Breaker: OPEN (${gpuSchedulerFailures}x) · Backoff ${gpuSchedulerBackoffMs}ms` : `Attempt ${gpuSchedulerFailures}/3 · Backoff ${gpuSchedulerBackoffMs}ms (±15% Jitter)` }}</span>
        </div>
        <button class="link-button" @click="refresh">重试</button>
      </div>
      <div v-if="gpuSchedulerState === 'loading' && !schedulerSnapshot" class="state-box panel" role="status">正在读取 Station GPU 双层账本与常驻实例…</div>
      <template v-else-if="schedulerSnapshot">
        <div v-if="!schedulerCards.length" class="state-box panel"><h2>尚未登记物理 GPU 账本</h2><p>等待宿主机 station-gpu-agent 上报物理卡拓扑与采样游标。</p></div>
        <div v-else class="gpu-ledger-grid">
          <article v-for="c in schedulerCards" :key="c.gpu_uuid" class="gpu-ledger-card" :data-gpu-card="c.gpu_index">
            <div class="card-heading">
              <div class="card-name">
                <div class="device-icon"><ResourceIcon name="gpu" /></div>
                <div>
                  <h3>GPU {{ c.gpu_index }} · {{ formatMiB(c.total_vram_mib) }}</h3>
                  <p>{{ c.model_name || 'NVIDIA GPU' }} · {{ c.node_name || c.node_uid }}</p>
                </div>
              </div>
              <span class="badge" :class="cardAdmission(c).badgeTone"><i></i>{{ cardAdmission(c).badgeLabel }}</span>
            </div>
            <div class="cursor-row" aria-label="双层对账游标">
              <span class="cursor-pill">ledger_event_seq <b>#{{ c.last_event_seq }}</b></span>
              <span class="cursor-pill">telemetry_sample_id <b>{{ c.telemetry_sample_id ? `#${c.telemetry_sample_id}` : '未提供' }}</b> · {{ c.telemetry_sampled_at ? sampleTime(c.telemetry_sampled_at) : '采样缺失' }}</span>
              <span class="cursor-pill">UUID <b>{{ c.gpu_uuid }}</b></span>
            </div>

            <div class="ledger-layer">
              <div class="ledger-layer-head">
                <strong>账本承诺层（Committed Ledger · 承诺上限）</strong>
                <span>承诺占用 <b>{{ formatMiB(c.pinned_mib + c.leased_mib + c.quarantined_mib) }}</b> · 账本未承诺额度 <b>{{ formatMiB(c.ledger_uncommitted_mib) }}</b> ({{ formatGiBFromMiB(c.ledger_uncommitted_mib) }})</span>
              </div>
              <div class="seg-bar" role="img" :aria-label="`GPU ${c.gpu_index} 账本承诺层分布`">
                <span class="seg-slice seg-driver" :style="{ width: segmentPercent(c.driver_overhead_mib, c.total_vram_mib) }" :title="`驱动底座 ${formatMiB(c.driver_overhead_mib)}`"></span>
                <span class="seg-slice seg-pinned" :style="{ width: segmentPercent(c.pinned_mib, c.total_vram_mib) }" :title="`人类保护 Pinned ${formatMiB(c.pinned_mib)}`"></span>
                <span class="seg-slice seg-leased" :style="{ width: segmentPercent(c.leased_mib, c.total_vram_mib) }" :title="`运行承诺 Leased ${formatMiB(c.leased_mib)}`"></span>
                <span v-if="c.quarantined_mib > 0" class="seg-slice seg-quarantine" :style="{ width: segmentPercent(c.quarantined_mib, c.total_vram_mib) }" :title="`隔离冻结 Quarantined ${formatMiB(c.quarantined_mib)}`"></span>
                <span class="seg-slice seg-headroom" :style="{ width: segmentPercent(c.headroom_mib, c.total_vram_mib) }" :title="`防爆余量 Headroom ${formatMiB(c.headroom_mib)}`"></span>
                <span class="seg-slice seg-free" :style="{ width: segmentPercent(c.ledger_uncommitted_mib, c.total_vram_mib) }" :title="`账本未承诺额度 ${formatMiB(c.ledger_uncommitted_mib)}`"></span>
              </div>
              <div class="seg-legend">
                <span><i class="seg-dot seg-driver"></i>驱动底座 {{ formatMiB(c.driver_overhead_mib) }}</span>
                <span><i class="seg-dot seg-pinned"></i>人类保护 Pinned {{ formatMiB(c.pinned_mib) }}</span>
                <span><i class="seg-dot seg-leased"></i>运行承诺 Leased {{ formatMiB(c.leased_mib) }}</span>
                <span v-if="c.quarantined_mib > 0"><i class="seg-dot seg-quarantine"></i>隔离冻结 {{ formatMiB(c.quarantined_mib) }}</span>
                <span><i class="seg-dot seg-headroom"></i>防爆余量 {{ formatMiB(c.headroom_mib) }}</span>
                <span>账本未承诺 {{ formatMiB(c.ledger_uncommitted_mib) }}</span>
              </div>
            </div>

            <div class="ledger-layer">
              <div class="ledger-layer-head">
                <strong>硬件实测层（Measured Telemetry · 宿主机 NVML）</strong>
                <span v-if="cardAdmission(c).closed" style="color:var(--amber)">{{ schedulerPollDegraded ? '账本轮询异常 · Fail-Closed 准入待确认' : '采样暂缺或过期 · Fail-Closed 已关闭该卡新准入' }}</span>
                <span v-else>实测已用 <b>{{ formatMiB(c.measured_used_vram_mib) }}</b> · NVML 空闲 <b>{{ formatMiB(c.measured_free_vram_mib) }}</b> ({{ formatGiBFromMiB(c.measured_free_vram_mib) }})</span>
              </div>
              <div v-if="cardAdmission(c).closed" style="font-size:11px;color:var(--muted)">{{ schedulerPollDegraded ? '当前 GPU 调度接口轮询失败，保留上一帧账本仅作参考，Fail-Closed 禁用显存变更操作。' : `未收到宿主机 station-gpu-agent 新鲜采样，不将未知物理显存渲染为零；账本承诺层仍按 PostgreSQL 游标 #${c.last_event_seq} 保持锁定。` }}</div>
              <template v-else>
                <div class="seg-bar" role="img" :aria-label="`GPU ${c.gpu_index} 硬件实测层分布`">
                  <span class="seg-slice seg-used" :style="{ width: segmentPercent(cardActiveUsedMiB(c), c.total_vram_mib) }" :title="`活跃显存 ${formatMiB(cardActiveUsedMiB(c))}`"></span>
                  <span class="seg-slice seg-evictable" :style="{ width: segmentPercent(c.evictable_mib, c.total_vram_mib) }" :title="`S2 可回收热缓存 ${formatMiB(c.evictable_mib)}`"></span>
                  <span class="seg-slice seg-free" :style="{ width: segmentPercent(c.measured_free_vram_mib, c.total_vram_mib) }" :title="`NVML 实测空闲 ${formatMiB(c.measured_free_vram_mib)}`"></span>
                </div>
                <div class="seg-legend">
                  <span><i class="seg-dot seg-used"></i>活跃显存 {{ formatMiB(cardActiveUsedMiB(c)) }} ({{ formatGiBFromMiB(cardActiveUsedMiB(c)) }})</span>
                  <span><i class="seg-dot seg-evictable"></i>可回收缓存 Evictable {{ formatMiB(c.evictable_mib) }} ({{ formatGiBFromMiB(c.evictable_mib) }})</span>
                  <span>NVML 空闲 {{ formatMiB(c.measured_free_vram_mib) }} ({{ formatGiBFromMiB(c.measured_free_vram_mib) }})</span>
                </div>
              </template>
            </div>

            <div class="barrier-strip" :aria-label="`GPU ${c.gpu_index} 准入净额 G_now 与回收潜力`">
              <template v-if="cardAdmission(c).closed">
                <span style="color:var(--amber)">当前可授予额度 G_now：<strong>不可判定（Fail-Closed 关闭新准入）</strong></span>
                <span>账本未承诺：<strong>{{ formatMiB(c.ledger_uncommitted_mib) }}</strong></span>
              </template>
              <template v-else>
                <span>当前可授予额度 G_now：<strong>{{ formatMiB(c.grantable_standard_mib) }}</strong> ({{ formatGiBFromMiB(c.grantable_standard_mib) }}) · 未兑现承诺扣减 {{ formatMiB(c.unmaterialized_budget_mib) }}</span>
                <span>回收潜力 V_evictable：<strong>+{{ formatMiB(c.evictable_mib) }}</strong><template v-if="c.evictable_mib > 0">（回收后可达 {{ formatMiB(c.grantable_standard_mib + c.evictable_mib) }}）</template></span>
              </template>
            </div>

            <div class="barrier-strip">
              <span>K8s 时间片名额 {{ c.allocated_k8s_slots + c.scaling_up_k8s_slots }}/{{ c.time_slicing_slots || 4 }} · 状态 {{ c.state }}</span>
              <span>预约屏障：<strong>{{ c.reserved_by_lease_id || '无' }}</strong> · 围栏 #{{ c.min_fencing_token }}</span>
            </div>
          </article>
        </div>

        <div class="section-head">
          <h2>排队队列与媒体应用「本地」渠道阻塞透传</h2>
          <span>从同一实时账本状态派生队列顺位、精确阻塞持有者与活跃租约</span>
        </div>
        <div class="queue-dual-grid">
          <section class="queue-card-panel" aria-label="Station Runtime 排队与清场队列">
            <div style="display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:12px;flex-wrap:wrap">
              <div>
                <strong style="font-size:13px">排队与主动清场队列（{{ queuedLeases.length }}）</strong>
                <p style="font-size:11px;color:var(--muted)">按优先级与到达序展示精确阻塞原因及目标物理卡</p>
              </div>
              <button class="button primary" :disabled="!pageActive || schedulerPollDegraded || !actionableEvictableResidencies.length || !viewerCanEvict || reclaimingAll" @click="reclaimAllEvictable">{{ reclaimingAll ? '回收中…' : `一键回收空闲 (${actionableEvictableResidencies.length})` }}</button>
            </div>
            <div v-if="!queuedLeases.length" style="padding:18px 0;font-size:12px;color:var(--muted)">当前无排队任务，双卡余量可直接准入。</div>
            <div v-for="(q, idx) in queuedLeases" :key="q.lease_id" class="queue-item">
              <div style="display:flex;gap:10px;min-width:0">
                <span class="queue-rank">#{{ q.queue_position || idx + 1 }}</span>
                <div style="min-width:0">
                  <div style="display:flex;align-items:center;gap:7px;flex-wrap:wrap">
                    <strong style="font-size:13px">{{ residencyAppLabel(q.holder_app_id) }} · {{ q.holder_task_id }}</strong>
                    <span class="channel-local-pill">本地</span>
                    <span class="badge" :class="q.state === 'draining' ? 'good' : 'warning'">{{ q.state === 'draining' ? 'DrainingPeer · 清场衔接中' : 'Queued · 排队等待' }}</span>
                    <span class="cursor-pill">{{ q.priority }} · 需 {{ formatGiBFromMiB(q.peak_budget_mib) }}</span>
                  </div>
                  <p style="margin-top:5px;font-size:11px;color:var(--muted);line-height:1.6">{{ blockingReasonLabel(q.blocking_reason, q.blocking_holder_id) }}</p>
                </div>
              </div>
              <span class="cursor-pill" style="flex:none">{{ leaseTargetCardLabel(q) }}</span>
            </div>
          </section>

          <section class="queue-card-panel" aria-label="当前持锁计算租约与本地渠道透传">
            <div style="margin-bottom:12px">
              <strong style="font-size:13px">活跃计算租约与「本地」渠道联动（{{ runningLeases.length }}）</strong>
              <p style="font-size:11px;color:var(--muted)">自托管引擎统一呈现「本地」渠道角标，阶段切换原子更新承诺预算</p>
            </div>
            <div v-if="!runningLeases.length" style="padding:18px 0;font-size:12px;color:var(--muted)">当前无运行中计算租约。</div>
            <div v-for="l in runningLeases" :key="l.lease_id" class="queue-item">
              <div style="min-width:0">
                <div style="display:flex;align-items:center;gap:7px;flex-wrap:wrap">
                  <strong style="font-size:12.5px">{{ residencyAppLabel(l.holder_app_id) }} · {{ l.holder_task_id }}</strong>
                  <span class="channel-local-pill">本地</span>
                  <span class="badge good">运行中 · {{ leaseTargetCardLabel(l) }} ({{ formatGiBFromMiB(l.peak_budget_mib) }})</span>
                </div>
                <p style="margin-top:5px;font-size:11px;color:var(--muted);line-height:1.55">阶段 {{ l.stage_id || 'default' }} · 围栏令牌 #{{ l.fencing_token }} · 租约 {{ l.lease_id }}</p>
              </div>
            </div>
          </section>
        </div>

        <div class="section-head">
          <h2>常驻平台实例与服务驻留明细（S0–S3）</h2>
          <span>S0 计算中（禁常规释放） · S1 视口使用中（保护） · S2 空闲热缓存（可回收） · S3 零显存休眠</span>
        </div>
        <div v-if="!schedulerResidencies.length" class="state-box panel"><h2>暂无常驻平台驻留记录</h2><p>ComfyUI 与 Blender 实例启动后将在此展示 S0–S3 驻留态与外层守护身份。</p></div>
        <div v-else class="table-panel panel">
          <table class="residency-table" aria-label="常驻实例与服务驻留明细">
            <thead>
              <tr>
                <th>实例 / 外层守护身份</th>
                <th>物理卡</th>
                <th>驻留安全状态（S0–S3）</th>
                <th>账本承诺 / 实测显存</th>
                <th>显存管控操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="r in schedulerResidencies" :key="r.instance_id">
                <td data-label="实例">
                  <span class="cell-main">{{ r.instance_id }} · <small style="font-weight:400;color:var(--muted)">{{ residencyAppLabel(r.app_id) }}</small></span>
                  <span class="cell-sub mono-code">boot={{ r.boot_id }} · epoch={{ r.gpu_epoch }} · seq={{ r.residency_seq }}</span>
                </td>
                <td data-label="物理卡">
                  <span class="cell-main">{{ residencyCardLabel(r) }}</span>
                  <span class="cell-sub">{{ residencyCardSub(r) }}</span>
                </td>
                <td data-label="驻留状态">
                  <div style="display:flex;align-items:center;gap:6px;flex-wrap:wrap">
                    <span class="badge" :class="residencyBadge(r).tone">{{ residencyBadge(r).label }}</span>
                    <span v-if="r.dirty" class="cursor-pill" title="存在未显式保存草稿变更，休眠自动写隔离检查点">dirty=true</span>
                  </div>
                  <span class="cell-sub">{{ residencyNote(r) }}</span>
                </td>
                <td data-label="显存" class="number num tabular-nums">
                  <span>{{ formatMiB(residencyCommittedMiB(r)) }} / {{ formatMiB(residencyMeasuredMiB(r)) }}</span>
                  <span class="cell-sub">承诺 / 实测 ({{ formatGiBFromMiB(residencyMeasuredMiB(r)) }})</span>
                </td>
                <td data-label="操作">
                  <div class="residency-actions">
                    <button v-if="residencyVisualState(r) === 'S2' && r.evictable" class="button icon-only evict-gpu" :disabled="Boolean(evictBlockReason(r)) || evictingIds.has(r.instance_id)" :title="evictBlockReason(r) || (evictingIds.has(r.instance_id) ? '释放显存中…' : '释放显存（保留外层 boot_id 与草稿，退回 S3 零显存待命）')" :aria-label="evictBlockReason(r) || (evictingIds.has(r.instance_id) ? '释放显存中' : '释放显存（保留外层 boot_id 与草稿，退回 S3 零显存待命）')" @click="evictResidency(r)">
                      <svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><rect x="5" y="5" width="14" height="14" rx="2"/><path d="M9 2v3m6-3v3M9 19v3m6-3v3M2 9h3m-3 6h3m14-6h3m-3 6h3"/><path d="M12 8.5v6m-2.5-2.5L12 14.5l2.5-2.5"/></svg>
                    </button>
                    <button v-else-if="residencyVisualState(r) === 'S1'" class="button icon-only evict-protected" :disabled="!pageActive || schedulerPollDegraded || !isResidencyCardFresh(r)" title="释放显存（受 S1 人类活跃视口免杀保护，点击查看拦截说明）" aria-label="释放显存（受保护）" @click="openBlockedEvict(r)">
                      <svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><rect x="5" y="5" width="14" height="14" rx="2"/><path d="M9 2v3m6-3v3M9 19v3m6-3v3M2 9h3m-3 6h3m14-6h3m-3 6h3"/><path d="M12 8.5v6m-2.5-2.5L12 14.5l2.5-2.5"/></svg>
                    </button>
                    <span v-else-if="residencyVisualState(r) === 'S0'" class="disabled-action" tabindex="0" title="实例正持有有效计算租约执行任务，常规释放已禁用；任务结项后自动退回 S2 可回收态">S0 计算中禁释 ⓘ</span>
                    <span v-else-if="residencyVisualState(r) === 'S3'" style="font-size:11px;color:var(--muted)">已零显存待命</span>
                    <span v-else-if="residencyVisualState(r) === 'QUARANTINED'" class="disabled-action" tabindex="0" title="显存回收失败已隔离冻结，常规释放不可用；请排查或由管理员执行强制清场">隔离冻结禁释 ⓘ</span>
                    <span v-else-if="residencyVisualState(r) === 'RECONCILING'" class="disabled-action" tabindex="0" title="实例正在对账恢复中，暂停常规释放">对账中禁释 ⓘ</span>
                    <span v-else-if="residencyVisualState(r) === 'TRANSITION'" class="disabled-action" tabindex="0" title="实例正处于唤醒、检查点封存或清场转换态，请稍候刷新">状态转换中…</span>
                    <span v-else class="disabled-action" tabindex="0" title="未知驻留状态，Fail-Closed 禁用常规释放">状态未确认 ⓘ</span>
                    <button v-if="viewerCanForceDrain && canAdminForceDrainResidency(r)" class="button danger" :disabled="Boolean(forceDrainBlockReason(r)) || forceDraining" :title="forceDrainBlockReason(r) || undefined" @click="openForceDrain(r)">强制清场</button>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
          <div class="table-caption" style="display:flex;justify-content:space-between;align-items:center;flex-wrap:wrap;gap:8px">
            <span v-if="schedulerEvents.length">最新按卡哈希链事件：GPU {{ schedulerEvents[0].gpu_index }} #{{ schedulerEvents[0].card_event_seq }} {{ schedulerEvents[0].reason_code }} · {{ schedulerEvents[0].entity_id }} ({{ schedulerEvents[0].from_state || 'init' }} → {{ schedulerEvents[0].to_state }})</span>
            <span v-else>按卡哈希链事件流水正常同步</span>
            <button v-if="schedulerEvents.length > 1" class="link-button" :aria-expanded="expandedAuditEvents" @click="expandedAuditEvents = !expandedAuditEvents">{{ expandedAuditEvents ? '收起审计流水' : `展开最近 ${schedulerEvents.length} 条审计流水` }} ⌄</button>
          </div>
          <div v-if="expandedAuditEvents && schedulerEvents.length" class="inline-detail" style="margin:0 14px 14px">
            <p v-for="ev in schedulerEvents" :key="ev.event_id" class="mono-code">GPU {{ ev.gpu_index }} #{{ ev.card_event_seq }} · {{ ev.reason_code }} · {{ ev.entity_type }}:{{ ev.entity_id }} ({{ ev.from_state || '—' }} → {{ ev.to_state }}) · {{ formatMiB(ev.budget_mib) }} · {{ sampleTime(ev.occurred_at) }}</p>
          </div>
        </div>
      </template>

      <div class="section-head"><h2>容器工作负载</h2><span>实际使用 / 请求 / 上限</span></div>
      <div class="filter-toolbar"><label class="search"><input v-model="taskSearch" aria-label="搜索工作负载" placeholder="搜索工作负载或命名空间…"></label><div class="chips"><button class="chip" :aria-pressed="taskFilter === 'all'" @click="taskFilter = 'all'">全部</button><button v-for="type in categories" :key="type" class="chip" :aria-pressed="taskFilter === type" @click="taskFilter = type">{{ categoryLabel(type) }}</button></div><select v-model="taskStatus" class="filter-select" aria-label="工作负载状态"><option value="all">全部状态</option><option v-for="(label,key) in groups" :key="key" :value="key">{{ label }}</option></select><span class="filter-status filter-tail">显示 {{ visibleTasks.length }} / {{ workloads.length }} 个</span></div>
      <div v-if="!visibleTasks.length" class="state-box panel"><h2>{{ workloads.length ? '没有符合条件的记录' : '暂无工作负载' }}</h2><p>监控对象是容器工作负载，生成任务请在所属应用查看。</p><button v-if="workloads.length" class="button" @click="clearTasks">清除筛选</button></div>
      <div v-else class="table-panel panel"><table class="task-table"><thead><tr><th>工作负载 / 命名空间</th><th>状态</th><th>GPU 声明</th><th>CPU 实际 / 请求 / 上限</th><th>内存 实际 / 请求 / 上限</th><th>详情</th></tr></thead><tbody><template v-for="w in visibleTasks" :key="workloadKey(w)"><tr class="task-row"><td data-label="工作负载"><span class="cell-main">{{ w.display_name || w.name }}</span><span class="cell-sub">{{ w.namespace }} · {{ categoryLabel(w.type) }}</span></td><td data-label="状态"><span class="badge" :class="statusGroup(w) === 'running' ? 'good' : statusGroup(w) === 'error' ? 'danger' : 'warning'">{{ groups[statusGroup(w)] }}</span><span class="cell-sub">{{ w.status }} · {{ w.containers.filter(c => c.ready).length }}/{{ w.containers.length }} 就绪</span></td><td data-label="GPU 声明" class="num tabular-nums">{{ gpuDeclaration(w) }}<span class="cell-sub">实测利用率 / 显存未提供</span></td><td data-label="CPU 实际 / 请求 / 上限" class="number num tabular-nums">{{ allocation(w, 'cpu') }}</td><td data-label="内存 实际 / 请求 / 上限" class="number num tabular-nums">{{ allocation(w, 'memory') }}</td><td data-label="详情"><button class="table-action" :aria-expanded="expandedTask.has(workloadKey(w))" @click="toggle(expandedTask, workloadKey(w))">{{ expandedTask.has(workloadKey(w)) ? '收起' : '展开' }} ⌄</button></td></tr><tr v-if="expandedTask.has(workloadKey(w))" class="row-detail"><td colspan="6"><div class="inline-detail"><p>Pod {{ w.pod_name || w.name }} · UID {{ w.pod_uid }} · 运行时长 {{ w.age }} · 节点未提供</p><div v-for="c in w.containers" :key="c.name" class="container-detail"><p><strong>{{ c.name }}</strong> · {{ c.ready ? '就绪' : '未就绪' }} · 重启 {{ c.restarts }} 次</p><p>CPU：实际 {{ metricUsage(c.cpu) }} / 请求 {{ formatResource(c.cpu.request) }} / 上限 {{ valid(c.cpu.limit) ? formatResource(c.cpu.limit) : '未设置' }} · {{ quality(c.cpu.quality) }} · {{ sampleTime(c.cpu.sampled_at) }}</p><p>内存：实际 {{ metricUsage(c.memory) }} / 请求 {{ formatResource(c.memory.request, 'bytes') }} / 上限 {{ valid(c.memory.limit) ? formatResource(c.memory.limit, 'bytes') : '未设置' }} · {{ quality(c.memory.quality) }} · {{ sampleTime(c.memory.sampled_at) }}</p><p>GPU 请求 {{ numeric(c.gpu_request) }} 份 / 上限 {{ numeric(c.gpu_limit) }} 份 · 实测显存未提供</p><p v-if="c.cpu.reason || c.memory.reason">观测原因：{{ c.cpu.reason || c.memory.reason }}</p></div></div></td></tr></template></tbody></table></div>
      <p class="table-note">只统计仍在运行、等待或失败的工作负载。已成功结束的一次性任务不列入当前任务。资源按容器归属统计，未观测值不按零计。GPU 声明份额不代表独占整卡，整卡显存不复制到各工作负载。</p>
    </template>
    <dialog ref="releaseDialog" id="release-workspace-dialog" aria-labelledby="release-title" @cancel.prevent="closeRelease">
      <form @submit.prevent="submitRelease"><div class="dialog-head"><h2 id="release-title">确认释放工作卷</h2><button type="button" class="button" :disabled="releasing" @click="closeRelease">取消</button></div><div class="dialog-kv"><p>PVC：<b>{{ releaseTarget?.pvc_name }}</b></p><p>PV：<b>{{ releaseTarget?.pv_name || '未提供' }}</b></p><p>所属应用：<b>{{ releaseTarget?.app_display_name || releaseTarget?.app_id }}</b></p></div><p class="release-warning">将清空该卷残留数据、删除 PVC 并解除绑定，使底层 PV 回到可分配状态。此操作无法撤销；不代表删除 PV 对象，也不承诺释放同等物理磁盘容量。</p><label class="field">请输入 PVC 名称确认<input v-model="releaseName" type="text" autocomplete="off" :disabled="releasing" :placeholder="releaseTarget?.pvc_name"></label><p v-if="releaseBlock || releaseError" class="release-warning" role="alert">{{ releaseError || releaseBlock }}</p><div class="dialog-actions"><button type="button" class="button" :disabled="releasing" @click="closeRelease">保留工作卷</button><button type="submit" class="button danger" :disabled="releasing || !!releaseBlock || !releaseTarget || releaseName !== releaseTarget.pvc_name">{{ releasing ? '释放中…' : '确认释放工作卷' }}</button></div></form>
    </dialog>
    <dialog ref="lockedModelDialog" id="locked-model-dialog" aria-labelledby="locked-model-title" @cancel.prevent="closeLockedModel">
      <div class="dialog-head"><h2 id="locked-model-title">409 MODEL_IN_USE · 引用锁禁删保护</h2><button type="button" class="button" @click="closeLockedModel">关闭</button></div>
      <div v-if="lockedModelTarget" class="dialog-kv">
        <p>模型编号：<b>{{ lockedModelTarget.catalog_code }}</b> · <b>{{ lockedModelTarget.display_name || lockedModelTarget.model_id }}</b></p>
        <p>容器视图：<b class="mono-code">/models/{{ lockedModelTarget.relative_view_path }}</b></p>
        <p>引用锁状态：<b>active_pins={{ lockedModelTarget.active_pins }} · reserved_pins={{ lockedModelTarget.reserved_pins }}</b></p>
      </div>
      <p class="release-warning">服务端保护拦截（409 MODEL_IN_USE）：该模型正被已发布工作流 API、运行中推理任务或活跃实例会话持有引用锁，禁止执行缓存淘汰或手动删除。请先下线关联工作流 API 或等待任务结项后再清理。</p>
      <div class="dialog-actions"><button type="button" class="button primary" @click="closeLockedModel">知道了</button></div>
    </dialog>
    <dialog ref="evictModelDialog" id="evict-model-dialog" aria-labelledby="evict-model-title" @cancel.prevent="closeEvictModel">
      <form @submit.prevent="submitEvictModel">
        <div class="dialog-head"><h2 id="evict-model-title">{{ evictModelTarget?.status === 'failed' ? '确认清理失败残留分片' : '确认清理本地模型缓存' }}</h2><button type="button" class="button" :disabled="evictingModel" @click="closeEvictModel">取消</button></div>
        <div v-if="evictModelTarget" class="dialog-kv">
          <p>模型编号：<b>{{ evictModelTarget.catalog_code }}</b> · <b>{{ evictModelTarget.display_name || evictModelTarget.model_id }}</b></p>
          <p>容器视图：<b class="mono-code">/models/{{ evictModelTarget.relative_view_path }}</b></p>
          <p v-if="evictModelTarget.status === 'failed'">残留分片：<b>{{ formatModelBytes(evictModelTarget.part_bytes_on_disk || evictModelTarget.bytes_downloaded) }}</b> / {{ formatModelBytes(evictModelTarget.size_bytes) }} · <b class="mono-code">sha256:{{ evictModelTarget.sha256.slice(0, 16) }}…</b></p>
          <p v-else>体积与校验：<b>{{ formatModelBytes(evictModelTarget.size_bytes) }}</b> · <b class="mono-code">sha256:{{ evictModelTarget.sha256.slice(0, 16) }}…</b></p>
        </div>
        <p v-if="evictModelTarget?.status === 'failed'">将安全回收该模型的断点续传残留分片（<code>.part</code>）并清空错误状态，释放本地磁盘空间。模型分类编号与目录登记元数据仍保留（退回 <code>not_installed</code> 状态），后续可通过 <code>model.ensure</code> 重新单飞拉取。</p>
        <p v-else>将原子卸载该模型的只读目录软链并回收底层 CAS 对象块，释放本地磁盘空间。模型分类编号与目录登记元数据仍保留（退回 <code>not_installed</code> 状态），后续可通过 <code>model.ensure</code> 重新单飞拉取。</p>
        <p v-if="evictModelError || evictModelBlock" class="release-warning" role="alert">{{ evictModelError || evictModelBlock }}</p>
        <div class="dialog-actions">
          <button type="button" class="button" :disabled="evictingModel" @click="closeEvictModel">{{ evictModelTarget?.status === 'failed' ? '暂不清理' : '保留缓存' }}</button>
          <button type="submit" class="button danger" :disabled="evictingModel || Boolean(evictModelBlock) || !evictModelTarget">{{ evictingModel ? '清理中…' : (evictModelTarget?.status === 'failed' ? '确认清理残留' : '确认清理缓存') }}</button>
        </div>
      </form>
    </dialog>
    <dialog ref="blockedEvictDialog" id="blocked-evict-dialog" aria-labelledby="blocked-evict-title" @cancel.prevent="closeBlockedEvict">
      <div class="dialog-head"><h2 id="blocked-evict-title">409 GUI_EDIT_LEASE_HELD · 人类活跃视口免杀保护</h2><button type="button" class="button" @click="closeBlockedEvict">关闭</button></div>
      <div v-if="blockedEvictTarget" class="dialog-kv">
        <p>目标实例：<b>{{ blockedEvictTarget.instance_id }}</b> ({{ residencyAppLabel(blockedEvictTarget.app_id) }})</p>
        <p>驻留状态：<b>{{ residencyBadge(blockedEvictTarget).label }}</b> · <b class="mono-code">boot_id={{ blockedEvictTarget.boot_id }}</b></p>
        <p>保护预算：<b>Pinned {{ formatMiB(blockedEvictTarget.pinned_budget_mib) }}</b></p>
      </div>
      <p class="release-warning">服务端安全红线拦截（409 GUI_EDIT_LEASE_HELD）：检测到活跃人类 WebRTC 视口连接心跳，常规「释放显存」与后台自动抢占均被严格拒绝，确保创作零丢稿。</p>
      <div class="dialog-actions"><button type="button" class="button primary" @click="closeBlockedEvict">知道了</button></div>
    </dialog>
    <dialog ref="forceDrainDialog" id="force-drain-dialog" aria-labelledby="force-drain-title" @cancel.prevent="closeForceDrain">
      <form @submit.prevent="submitForceDrain">
        <div class="dialog-head"><h2 id="force-drain-title">管理员二次确认 · 强制清场显存上下文</h2><button type="button" class="button" :disabled="forceDraining" @click="closeForceDrain">取消</button></div>
        <div v-if="forceDrainTarget" class="dialog-kv">
          <p>目标常驻实例：<b>{{ forceDrainTarget.instance_id }}</b> ({{ residencyAppLabel(forceDrainTarget.app_id) }})</p>
          <p>当前驻留态：<b>{{ residencyBadge(forceDrainTarget).label }}</b> · 物理卡 <b>{{ residencyCardLabel(forceDrainTarget) }}</b></p>
          <p>外层守护身份：<b class="mono-code">boot_id={{ forceDrainTarget.boot_id }} · gpu_epoch={{ forceDrainTarget.gpu_epoch }}</b></p>
        </div>
        <p>仅限组织管理员执行：将强制收割该实例当前内层 GPU 子进程树并退回 <code>S3</code> 零显存待命（保留外层容器与 <code>boot_id</code>），同时在 <code>station.gpu_scheduler_events</code> 追加不可变哈希链审计记录。</p>
        <label class="check-row">
          <input v-model="forceDrainAck" type="checkbox" :disabled="forceDraining">
          <span>我已知晓强制清场会中断该实例当前 GPU 上下文（若存在未提交渲染或内存态 Undo 历史将丢失），并确认以管理员身份写入不可变审计日志。</span>
        </label>
        <label class="field">请输入实例标识 <code>{{ forceDrainTarget?.instance_id }}</code> 完成二次确认
          <input v-model="forceDrainInput" type="text" autocomplete="off" spellcheck="false" :disabled="forceDraining" :placeholder="forceDrainTarget?.instance_id">
        </label>
        <p v-if="forceDrainError || forceDrainBlock" class="release-warning" role="alert">{{ forceDrainError || forceDrainBlock }}</p>
        <div class="dialog-actions">
          <button type="button" class="button" :disabled="forceDraining" @click="closeForceDrain">取消</button>
          <button type="submit" class="button danger" :disabled="forceDraining || Boolean(forceDrainBlock) || !forceDrainTarget || !forceDrainAck || forceDrainInput.trim() !== forceDrainTarget.instance_id">{{ forceDraining ? '清场中…' : '确认强制清场并记录审计' }}</button>
        </div>
      </form>
    </dialog>
  </main>
</template>
