<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import ResourceIcon from './ResourceIcon.vue'
import { contextScope, hostProps } from './sdk'
import type { AppSDK, DashboardHost } from './sdk'
import { createPolling, createScopedPolling } from './polling'
import { formatResource, isWorkspacesResponse, meter, numeric, parseResourceView, ratio, scaled, valid } from './model'
import type { Dashboard, ResourceView, WorkspaceVolume, Workload } from './model'
import { allocation, metricUsage, releaseReason, statusGroup, workloadKey, workspaceKey } from './presentation'
import { createReleaseIntents, isReleased } from './release'

const dashboard = ref<Dashboard | null>(null), state = ref<'loading' | 'ready' | 'error'>('loading'), error = ref('')
const currentView = ref<ResourceView>('monitor'), refreshing = ref(false), toast = ref('')
const volumes = ref<WorkspaceVolume[] | null>(null), volumeState = ref<'loading' | 'ready' | 'error'>('loading'), volumeError = ref('')
const volumeSearch = ref(''), volumeFilter = ref('all'), taskSearch = ref(''), taskFilter = ref('all'), taskStatus = ref('all')
const expandedGPU = ref(new Set<string>()), expandedHost = ref(false), expandedVolume = ref(new Set<string>()), expandedTask = ref(new Set<string>())
const meta = {
  monitor: { title: '资源监控', description: '查看 GPU、CPU、内存与存储使用情况。' },
  storage: { title: '存储管理', description: '查看磁盘容量与应用工作卷，释放不再保留的工作卷。' },
  tasks: { title: '任务监控', description: '查看容器工作负载的运行状态、资源配额与实际使用情况。' },
}
const summary = computed(() => dashboard.value?.summary ?? {}), node = computed(() => dashboard.value?.node)
const gpus = computed(() => dashboard.value?.gpus ?? []), disks = computed(() => node.value?.storage_disks ?? [])
const workloads = computed(() => dashboard.value?.workloads ?? [])
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
function clearTasks() { taskSearch.value = ''; taskFilter.value = 'all'; taskStatus.value = 'all' }
function gpuDeclaration(w: Workload) {
  return w.containers.length && w.containers.every(c => valid(c.gpu_request)) ? numeric(w.containers.reduce((s,c) => s + c.gpu_request!, 0)) + ' 份' : '未提供'
}

let sdk: AppSDK | undefined, host: DashboardHost | undefined, polling: ReturnType<typeof createPolling> | undefined
let volumePolling: ReturnType<typeof createScopedPolling<{ workspaces: WorkspaceVolume[] }>> | undefined
let observer: ResizeObserver | undefined, disposed = false, active = true, scopeEpoch = 0
let scope: string | null = null
const subscriptions: (() => void)[] = [], scrolls: Record<ResourceView, number> = { monitor: 0, storage: 0, tasks: 0 }
let restoreFrame = 0
async function changeView(next: ResourceView) {
  if (next === currentView.value) return
  scrolls[currentView.value] = host?.getScroll?.() ?? 0
  currentView.value = next
  volumePolling?.setEnabled(next === 'storage')
  await nextTick()
  host?.resize(Math.ceil(document.getElementById('app')!.getBoundingClientRect().height))
  cancelAnimationFrame(restoreFrame)
  restoreFrame = requestAnimationFrame(() => { if (!disposed && active) host?.restoreScroll?.(scrolls[next]) })
}
async function navigateStorage() { await sdk?.navigation?.request?.({ target: 'app', path: '/storage' }) }
async function refresh() {
  refreshing.value = true
  try { await Promise.all([polling?.refresh(), currentView.value === 'storage' ? volumePolling?.refresh() : undefined]) }
  finally { refreshing.value = false }
}
function publishCounts() {
  const detail = { workspaces: volumes.value?.length ?? null, tasks: dashboard.value?.workloads.length ?? null }
  window.dispatchEvent(new CustomEvent('studio:resources-counts', { detail }))
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
  clearVolumes(); clearTasks(); expandedGPU.value.clear(); expandedVolume.value.clear(); expandedTask.value.clear(); expandedHost.value = false
  for (const key of Object.keys(scrolls) as ResourceView[]) scrolls[key] = 0
}
function cleanup() {
  if (disposed) return
  disposed = true; scopeEpoch++; polling?.dispose(); volumePolling?.dispose(); observer?.disconnect()
  cancelAnimationFrame(restoreFrame); subscriptions.splice(0).forEach(stop => stop())
  releaseDialog.value?.close(); releaseIntents.clear()
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
    if (event === 'dispose') cleanup()
  }))
  polling = createPolling(sdk, result => { state.value = result.state; dashboard.value = result.data; error.value = result.message; publishCounts() })
  volumePolling = createScopedPolling(sdk, '/api/v1/workspaces', isWorkspacesResponse, result => {
    volumeState.value = result.state; volumes.value = result.data?.workspaces ?? null; volumeError.value = result.message; publishCounts()
  }, undefined, currentView.value === 'storage')
  const root = document.getElementById('app')!
  observer = new ResizeObserver(() => host?.resize(Math.ceil(root.getBoundingClientRect().height)))
  observer.observe(root); host.ready(); void refresh()
})
onBeforeUnmount(cleanup)
</script>

<template>
  <main class="resource-page" data-embedded="true">
    <header class="page-heading"><h1>{{ meta[currentView].title }}</h1><p>{{ meta[currentView].description }}</p></header>
    <div class="telemetry-toolbar"><span class="sample-state"><i class="status-dot" :class="{ warning: error }"></i>{{ error ? '更新失败 · 上次观测' : '观测时间' }} · {{ sampleTime(dashboard?.observed_at) }}</span><button class="button" :disabled="refreshing" @click="refresh">{{ refreshing ? '刷新中…' : '刷新' }}</button></div>
    <p v-if="toast" class="notice" role="status">{{ toast }}</p>
    <p v-if="error" class="notice" role="alert">{{ error }}<button class="link-button" @click="refresh">重试</button></p>
    <div v-if="state === 'loading' && !dashboard" class="kpi-grid" aria-label="正在读取资源观测" role="status"><div v-for="i in 4" :key="i" class="skeleton-card"><span></span><span></span></div></div>
    <div v-else-if="!dashboard" class="state-box"><h2>资源观测暂不可用</h2><p>请确认 Station 连接与当前查看权限。</p><button class="button" @click="refresh">重新读取</button></div>
    <template v-if="dashboard && currentView === 'monitor'">
      <p v-if="hardwareNotice" class="notice" role="status">{{ hardwareNotice }}</p>
      <div class="kpi-grid">
        <article class="kpi"><div class="kpi-label">GPU 设备<ResourceIcon name="gpu" /></div><div class="kpi-value">{{ numeric(summary.total_gpus, 0) }}<small>张</small></div><div class="kpi-foot">{{ dashboard.gpus === null ? '设备采样不可用，覆盖范围未提供' : `已监测 ${gpus.length} 张` }}</div><div class="kpi-foot">显存 {{ scaled(summary.used_vram_mb, 1024) }} / {{ scaled(summary.total_vram_mb, 1024) }} GiB</div></article>
        <article class="kpi"><div class="kpi-label">CPU 使用率<ResourceIcon name="cpu" /></div><div class="kpi-value">{{ numeric(summary.host_cpu_utilization) }}<small>%</small></div><div class="kpi-foot">{{ numeric(summary.host_cpu_cores, 0) }} vCPU <span>1 分钟负载 {{ numeric(summary.host_cpu_load1, 2) }}</span></div></article>
        <article class="kpi"><div class="kpi-label">系统内存<ResourceIcon name="memory" /></div><div class="kpi-value">{{ numeric(summary.host_mem_used_gb) }}<small>/ {{ numeric(summary.host_mem_total_gb) }} GiB</small></div><div class="kpi-foot">使用率 {{ numeric(summary.host_mem_used_percent) }}%</div><div class="meter blue"><span :style="meter(summary.host_mem_used_percent)"></span></div></article>
        <article class="kpi"><div class="kpi-label">存储使用量<ResourceIcon name="disk" /></div><div class="kpi-value storage-value">查看挂载点</div><div class="kpi-foot">汇总去重范围未提供</div><button class="link-button" @click="navigateStorage">查看存储管理 →</button></article>
      </div>
      <div class="section-head"><h2>GPU 设备</h2><span>{{ dashboard.gpus === null ? '采样不可用' : `已监测 ${gpus.length} 张` }}</span></div>
      <div v-if="!gpus.length" class="state-box"><h2>{{ dashboard.gpus === null ? 'GPU 观测暂不可用' : '尚未发现 GPU 设备' }}</h2><p>以 Station 返回的设备与采样状态为准。</p></div>
      <div class="resource-grid">
        <article v-for="g in gpus" :key="gpuKey(g)" class="resource-card">
          <div class="card-heading"><div class="card-name"><div class="device-icon"><ResourceIcon name="gpu" /></div><div><h3>GPU {{ g.index }} · {{ g.model_name || g.device }}</h3><p>GPU 利用率 {{ numeric(g.utilization) }}%</p></div></div><span class="badge" :class="g.quality === 'fresh' ? 'good' : 'warning'"><i></i>{{ quality(g.quality) }}</span></div>
          <div class="metric-line"><span>显存占用</span><strong>{{ scaled(g.used_vram_mb, 1024) }} <small>/ {{ scaled(g.total_vram_mb, 1024) }} GiB</small></strong></div>
          <div class="meter"><span :style="meter(ratio(g.used_vram_mb, g.total_vram_mb))"></span></div><div class="metric-caption"><span>显存容量使用率 {{ numeric(ratio(g.used_vram_mb, g.total_vram_mb)) }}%</span><span>空闲 {{ scaled(g.free_vram_mb, 1024) }} GiB</span></div>
          <div class="metric-split two"><div><small>温度</small><b>{{ numeric(g.temperature_c) }} °C</b></div><div><small>功耗</small><b>{{ numeric(g.power_watts) }} W</b></div></div>
          <div class="card-bottom"><span>采样 {{ sampleTime(g.sampled_at) }}</span><button class="link-button" :aria-expanded="expandedGPU.has(gpuKey(g))" @click="toggle(expandedGPU, gpuKey(g))">{{ expandedGPU.has(gpuKey(g)) ? '收起详情' : '展开详情' }} ⌄</button></div>
          <div v-if="expandedGPU.has(gpuKey(g))" class="inline-detail"><p>设备 UUID：{{ g.uuid || '未提供' }}</p><p>设备标识：{{ g.device || '未提供' }}</p><p>节点 / 硬件错误信息：未提供</p><p>采样质量仅表示观测状态；空闲显存不表示任务准入。</p></div>
        </article>
      </div>
      <div class="section-head"><h2>主机资源</h2><span>{{ quality(node?.quality) }} · {{ sampleTime(node?.sampled_at) }}</span></div>
      <div class="resource-grid">
        <article class="resource-card"><div class="card-heading"><div class="card-name"><div class="device-icon"><ResourceIcon name="cpu" /></div><h3>CPU</h3></div><span class="badge">{{ numeric(node?.cpu_cores, 0) }} vCPU</span></div><div class="metric-line"><span>CPU 使用率</span><strong>{{ numeric(node?.cpu_utilization_percent) }}<small>%</small></strong></div><div class="meter"><span :style="meter(node?.cpu_utilization_percent)"></span></div><div class="metric-split"><div v-for="(value, i) in [node?.cpu_load1, node?.cpu_load5, node?.cpu_load15]" :key="i"><small>系统负载 · {{ [1,5,15][i] }} 分钟</small><b>{{ numeric(value, 2) }}</b></div></div><div class="card-bottom"><span>CPU 百分比与系统负载为不同口径</span><button class="link-button" :aria-expanded="expandedHost" @click="expandedHost = !expandedHost">节点明细 ⌄</button></div><div v-if="expandedHost" class="inline-detail">节点名称：未提供 · 采样 {{ sampleTime(node?.sampled_at) }}</div></article>
        <article class="resource-card"><div class="card-heading"><div class="card-name"><div class="device-icon"><ResourceIcon name="memory" /></div><h3>系统内存</h3></div><span class="badge">{{ numeric(node?.mem_used_percent) }}%</span></div><div class="metric-line"><span>已用 / 总量</span><strong>{{ scaled(node?.mem_used_bytes, 1024 ** 3) }}<small>/ {{ scaled(node?.mem_total_bytes, 1024 ** 3) }} GiB</small></strong></div><div class="meter blue"><span :style="meter(node?.mem_used_percent)"></span></div><div class="metric-split two"><div><small>可用</small><b>{{ formatResource(node?.mem_available_bytes, 'bytes') }}</b></div><div><small>文件缓存</small><b>{{ formatResource(node?.mem_cached_bytes, 'bytes') }}</b></div></div><div class="card-bottom"><span>交换区：未提供</span><button class="link-button" :aria-expanded="expandedHost" @click="expandedHost = !expandedHost">节点明细 ⌄</button></div><div v-if="expandedHost" class="inline-detail"><p>缓冲区 {{ formatResource(node?.mem_buffers_bytes, 'bytes') }}</p><p>使用量遵循服务端统计口径，缓存与缓冲区不重复相加。</p></div></article>
      </div>
    </template>
    <template v-if="currentView === 'storage'">
      <p v-if="hardwareNotice" class="notice">{{ hardwareNotice }}</p>
      <div class="section-head"><h2>磁盘容量</h2><span>物理容量与工作卷预留容量分别统计</span></div>
      <div v-if="!disks.length" class="state-box"><h2>{{ node?.storage_disks?.length === 0 ? '暂无挂载点' : '磁盘观测暂不可用' }}</h2><p>接口未返回容量时不展示示例磁盘。</p></div>
      <div class="resource-grid"><article v-for="d in disks" :key="d.device + d.mountpoint" class="resource-card"><div class="card-heading"><div class="card-name"><div class="device-icon"><ResourceIcon name="disk" /></div><div><h3>{{ d.mountpoint === '/' ? '系统盘' : d.mountpoint === '/data' ? '数据盘' : '挂载点' }} · {{ d.mountpoint }}</h3><p>{{ d.device || '设备未提供' }} · {{ d.fstype || '文件系统未提供' }}</p></div></div><span class="badge">{{ numeric(d.used_percent) }}%</span></div><div class="metric-line"><span>已用 / 总量</span><strong>{{ scaled(d.used_bytes, 1024 ** 3) }}<small>GiB / {{ scaled(d.total_bytes, 1024 ** 4, 2) }} TiB</small></strong></div><div class="meter"><span :style="meter(d.used_percent)"></span></div><div class="metric-caption"><span>可用 {{ formatResource(d.avail_bytes, 'bytes') }}</span><span>容量使用率 {{ numeric(d.used_percent) }}%</span></div><div class="card-bottom">采样 {{ sampleTime(node?.sampled_at) }} · 节点未提供</div></article></div>
      <div class="section-head"><h2>应用工作卷</h2><span>{{ volumes?.length ?? '—' }} 个 · 待释放 {{ volumes ? volumeCounts.retained : '—' }} 个</span></div>
      <div class="filter-toolbar"><label class="search"><input v-model="volumeSearch" aria-label="搜索工作卷" placeholder="搜索 PVC、PV、应用或实例…"></label><div class="chips"><button v-for="key in ['all','in_use','bound_stopped','retained']" :key="key" class="chip" :aria-pressed="volumeFilter === key" @click="volumeFilter = key">{{ key === 'all' ? '全部' : volumeLabels[key] }} {{ volumes ? volumeCounts[key] : '—' }}</button></div><span class="filter-status filter-tail">显示 {{ visibleVolumes.length }} / {{ volumes?.length ?? '—' }} 个</span></div>
      <p v-if="volumeError" class="notice" role="alert">{{ volumeError }}<button class="link-button" @click="refresh">重试</button></p>
      <div v-if="volumeState === 'loading' && !volumes" class="state-box" role="status">正在读取应用工作卷…</div>
      <div v-else-if="!volumes" class="state-box"><h2>工作卷台账暂不可用</h2><p>磁盘观测独立显示，请确认台账查看权限后重试。</p></div>
      <div v-else-if="!visibleVolumes.length" class="state-box"><h2>{{ volumes.length ? '没有符合条件的记录' : '暂无应用工作卷' }}</h2><p>{{ volumes.length ? '试试其他关键词或状态。' : '创建实例后，工作卷按现行卷池策略分配。' }}</p><button v-if="volumes.length" class="button" @click="clearVolumes">清除筛选</button></div>
      <div v-else class="table-panel"><table class="volume-table"><thead><tr><th>PVC</th><th>PV</th><th>所属应用 / 卷池</th><th>关联实例</th><th>状态</th><th>预留容量</th><th>操作</th></tr></thead><tbody><template v-for="w in visibleVolumes" :key="workspaceKey(w)"><tr><td data-label="PVC"><button class="link-button cell-main" :aria-expanded="expandedVolume.has(workspaceKey(w))" @click="toggle(expandedVolume, workspaceKey(w))">{{ w.pvc_name }}</button><span class="cell-sub">{{ w.namespace }}</span></td><td data-label="PV">{{ w.pv_name || '未提供' }}</td><td data-label="所属应用 / 卷池"><span class="cell-main">{{ w.app_display_name || w.app_id || '未提供' }}</span><span class="cell-sub">{{ w.pool_name || '未提供' }}</span></td><td data-label="关联实例">{{ w.instance_name || (w.state === 'retained' ? '历史实例（已销毁）' : w.instance_id || '未提供') }}</td><td data-label="状态"><span class="badge" :class="w.state === 'in_use' ? 'good' : w.state === 'retained' ? 'warning' : ''">{{ volumeLabels[w.state] || '未知状态' }}</span></td><td data-label="预留容量" class="number">{{ formatResource(w.storage_bytes, 'bytes') }}</td><td data-label="操作"><button v-if="!releaseReason(w) && !error && !volumeError" class="table-action release" @click="openRelease(w)">释放工作卷</button><span v-else class="disabled-action" tabindex="0" :title="releaseReason(w) || '当前数据未确认，请刷新'">{{ releaseReason(w) === '无释放权限' ? '无释放权限' : '释放工作卷（不可用）' }}<span class="cell-sub">{{ releaseReason(w) || '当前数据未确认' }}</span></span></td></tr><tr v-if="expandedVolume.has(workspaceKey(w))" class="row-detail"><td colspan="7"><div class="inline-detail"><p>PVC：{{ w.namespace }}/{{ w.pvc_name }} · PV：{{ w.pv_name }}</p><p>卷池 {{ w.pool_name }} · 存储类 {{ w.storage_class || '未提供' }} · 访问模式 {{ w.access_mode || '未提供' }}</p><p>节点 {{ w.node_name || '未提供' }} · 实例 {{ w.instance_id || '未提供' }}</p><p>绑定 {{ sampleTime(w.bound_at) }} · 保留 {{ sampleTime(w.retained_at) }}</p></div></td></tr></template></tbody></table></div>
      <p class="table-note">预留容量是工作卷声明容量，不代表物理磁盘实际占用；销毁实例不会自动释放工作卷。</p>
    </template>
    <template v-if="dashboard && currentView === 'tasks'">
      <div class="kpi-grid"><article class="kpi"><div class="kpi-label">工作负载</div><div class="kpi-value">{{ workloads.length }}<small>个</small></div><div class="kpi-foot">当前可见容器工作负载</div></article><article v-for="key in (['running','waiting','error'] as const)" :key="key" class="kpi"><div class="kpi-label">{{ groups[key] }}</div><div class="kpi-value">{{ taskCounts[key] }}<small>个</small></div><div class="kpi-foot">按服务端状态与容器就绪情况</div></article><article v-if="taskCounts.other" class="kpi"><div class="kpi-label">其他状态</div><div class="kpi-value">{{ taskCounts.other }}<small>个</small></div><div class="kpi-foot">已结束、未知或其他状态</div></article></div>
      <div class="section-head"><h2>容器工作负载</h2><span>实际使用 / 请求 / 上限</span></div>
      <div class="filter-toolbar"><label class="search"><input v-model="taskSearch" aria-label="搜索工作负载" placeholder="搜索工作负载或命名空间…"></label><div class="chips"><button class="chip" :aria-pressed="taskFilter === 'all'" @click="taskFilter = 'all'">全部</button><button v-for="type in categories" :key="type" class="chip" :aria-pressed="taskFilter === type" @click="taskFilter = type">{{ categoryLabel(type) }}</button></div><select v-model="taskStatus" class="filter-select" aria-label="工作负载状态"><option value="all">全部状态</option><option v-for="(label,key) in groups" :key="key" :value="key">{{ label }}</option></select><span class="filter-status filter-tail">显示 {{ visibleTasks.length }} / {{ workloads.length }} 个</span></div>
      <div v-if="!visibleTasks.length" class="state-box"><h2>{{ workloads.length ? '没有符合条件的记录' : '暂无工作负载' }}</h2><p>监控对象是容器工作负载，生成任务请在所属应用查看。</p><button v-if="workloads.length" class="button" @click="clearTasks">清除筛选</button></div>
      <div v-else class="table-panel"><table class="task-table"><thead><tr><th>工作负载 / 命名空间</th><th>状态</th><th>GPU 声明</th><th>CPU 实际 / 请求 / 上限</th><th>内存 实际 / 请求 / 上限</th><th>详情</th></tr></thead><tbody><template v-for="w in visibleTasks" :key="workloadKey(w)"><tr><td data-label="工作负载"><span class="cell-main">{{ w.display_name || w.name }}</span><span class="cell-sub">{{ w.namespace }} · {{ categoryLabel(w.type) }}</span></td><td data-label="状态"><span class="badge" :class="statusGroup(w) === 'running' ? 'good' : statusGroup(w) === 'error' ? 'danger' : 'warning'">{{ groups[statusGroup(w)] }}</span><span class="cell-sub">{{ w.status }} · {{ w.containers.filter(c => c.ready).length }}/{{ w.containers.length }} 就绪</span></td><td data-label="GPU 声明">{{ gpuDeclaration(w) }}<span class="cell-sub">实测利用率 / 显存未提供</span></td><td data-label="CPU 实际 / 请求 / 上限" class="number">{{ allocation(w, 'cpu') }}</td><td data-label="内存 实际 / 请求 / 上限" class="number">{{ allocation(w, 'memory') }}</td><td data-label="详情"><button class="table-action" :aria-expanded="expandedTask.has(workloadKey(w))" @click="toggle(expandedTask, workloadKey(w))">{{ expandedTask.has(workloadKey(w)) ? '收起' : '展开' }} ⌄</button></td></tr><tr v-if="expandedTask.has(workloadKey(w))" class="row-detail"><td colspan="6"><div class="inline-detail"><p>Pod {{ w.pod_name || w.name }} · UID {{ w.pod_uid }} · 运行时长 {{ w.age }} · 节点未提供</p><div v-for="c in w.containers" :key="c.name" class="container-detail"><p><strong>{{ c.name }}</strong> · {{ c.ready ? '就绪' : '未就绪' }} · 重启 {{ c.restarts }} 次</p><p>CPU：实际 {{ metricUsage(c.cpu) }} / 请求 {{ formatResource(c.cpu.request) }} / 上限 {{ valid(c.cpu.limit) ? formatResource(c.cpu.limit) : '未设置' }} · {{ quality(c.cpu.quality) }} · {{ sampleTime(c.cpu.sampled_at) }}</p><p>内存：实际 {{ metricUsage(c.memory) }} / 请求 {{ formatResource(c.memory.request, 'bytes') }} / 上限 {{ valid(c.memory.limit) ? formatResource(c.memory.limit, 'bytes') : '未设置' }} · {{ quality(c.memory.quality) }} · {{ sampleTime(c.memory.sampled_at) }}</p><p>GPU 请求 {{ numeric(c.gpu_request) }} 份 / 上限 {{ numeric(c.gpu_limit) }} 份 · 实测显存未提供</p><p v-if="c.cpu.reason || c.memory.reason">观测原因：{{ c.cpu.reason || c.memory.reason }}</p></div></div></td></tr></template></tbody></table></div>
      <p class="table-note">资源按容器归属统计，未观测值不按零计。GPU 声明份额不代表独占整卡，整卡显存不复制到各工作负载。</p>
    </template>
    <dialog ref="releaseDialog" id="release-workspace-dialog" aria-labelledby="release-title" @cancel.prevent="closeRelease">
      <form @submit.prevent="submitRelease"><div class="dialog-head"><h2 id="release-title">确认释放工作卷</h2><button type="button" class="button" :disabled="releasing" @click="closeRelease">取消</button></div><div class="dialog-kv"><p>PVC：<b>{{ releaseTarget?.pvc_name }}</b></p><p>PV：<b>{{ releaseTarget?.pv_name || '未提供' }}</b></p><p>所属应用：<b>{{ releaseTarget?.app_display_name || releaseTarget?.app_id }}</b></p></div><p class="release-warning">将清空该卷残留数据、删除 PVC 并解除绑定，使底层 PV 回到可分配状态。此操作无法撤销；不代表删除 PV 对象，也不承诺释放同等物理磁盘容量。</p><label class="field">请输入 PVC 名称确认<input v-model="releaseName" type="text" autocomplete="off" :disabled="releasing" :placeholder="releaseTarget?.pvc_name"></label><p v-if="releaseBlock || releaseError" class="release-warning" role="alert">{{ releaseError || releaseBlock }}</p><div class="dialog-actions"><button type="button" class="button" :disabled="releasing" @click="closeRelease">保留工作卷</button><button type="submit" class="button danger" :disabled="releasing || !!releaseBlock || !releaseTarget || releaseName !== releaseTarget.pvc_name">{{ releasing ? '释放中…' : '确认释放工作卷' }}</button></div></form>
    </dialog>
  </main>
</template>
