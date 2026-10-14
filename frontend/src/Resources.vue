<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { hostProps } from './sdk'
import type { AppSDK } from './sdk'
import { createPolling } from './polling'
import {
  formatResource,
  isWorkspacesResponse,
  meter,
  numeric,
  parseResourceView,
  ratio,
  resourceState,
  resourceValue,
  scaled,
  valid
} from './model'
import type { Dashboard, Disk, ResourceView, WorkspaceVolume } from './model'

const dashboard = ref<Dashboard | null>(null)
const state = ref<'loading' | 'ready' | 'error'>('loading')
const error = ref('')
const currentView = ref<ResourceView>('monitor')
const currentFilter = ref<'all' | 'gpu' | 'infra'>('all')
const searchQuery = ref('')
const refreshing = ref(false)
const toastMessage = ref('')

const viewMeta = {
  monitor: {
    title: '资源监控',
    subtitle: 'Station 节点 GPU 拓扑、CPU 算力与系统运行内存实时遥测。',
    refreshLabel: '刷新遥测'
  },
  storage: {
    title: '存储管理',
    subtitle: 'Station 节点物理存储水位与全局应用工作卷池（PVC）绑定、释放回收。',
    refreshLabel: '刷新存储'
  },
  tasks: {
    title: '任务监控',
    subtitle: 'Station 已部署容器工作负载、GPU 创作任务配额与实际资源占用对账。',
    refreshLabel: '刷新任务'
  }
} as const

const summary = computed(() => dashboard.value?.summary ?? {})
const node = computed(() => dashboard.value?.node ?? null)
const gpus = computed(() => dashboard.value?.gpus ?? [])
const workloads = computed(() => dashboard.value?.workloads ?? [])
const hardwareNotice = computed(() =>
  dashboard.value?.hardware_state === 'stale'
    ? '硬件采样已过期，等待新观测。'
    : dashboard.value?.hardware_state === 'unavailable'
      ? '硬件观测暂不可用。'
      : ''
)

const rootDisk = computed<Disk>(() => {
  return (
    node.value?.storage_disks?.find(d => d.mountpoint === '/') ?? {
      mountpoint: '/',
      device: '',
      fstype: 'ext4',
      total_bytes: 3500000000000,
      avail_bytes: 3220000000000,
      used_bytes: 280000000000,
      used_percent: 8
    }
  )
})

const dataDisk = computed<Disk>(() => {
  return (
    node.value?.storage_disks?.find(d => d.mountpoint === '/data') ?? {
      mountpoint: '/data',
      device: '',
      fstype: 'ext4',
      total_bytes: 3700000000000,
      avail_bytes: 3434000000000,
      used_bytes: 266000000000,
      used_percent: 7
    }
  )
})

const workloadCounts = computed(() => {
  let gpu = 0
  let infra = 0
  for (const wl of workloads.value) {
    if (wl.type === 'gpu') gpu += 1
    else infra += 1
  }
  return { all: workloads.value.length, gpu, infra }
})

const filteredWorkloads = computed(() => {
  const q = searchQuery.value.trim().toLocaleLowerCase()
  return workloads.value.filter(
    w =>
      (currentFilter.value === 'all' || w.type === currentFilter.value) &&
      `${w.name} ${w.display_name} ${w.namespace}`.toLocaleLowerCase().includes(q)
  )
})

// Workspace volume pool state
const workspaces = ref<WorkspaceVolume[]>([])
const workspacesState = ref<'loading' | 'ready' | 'error'>('loading')
const wsFilter = ref<'all' | 'in_use' | 'bound_stopped' | 'retained'>('all')
const wsSearchQuery = ref('')

const wsCounts = computed(() => {
  const counts = { all: 0, in_use: 0, bound_stopped: 0, retained: 0 }
  for (const ws of workspaces.value) {
    counts.all += 1
    if (ws.state in counts) {
      counts[ws.state as keyof typeof counts] += 1
    }
  }
  return counts
})

const filteredWorkspaces = computed(() => {
  const q = wsSearchQuery.value.trim().toLocaleLowerCase()
  return workspaces.value.filter(ws => {
    const stateMatch = wsFilter.value === 'all' || ws.state === wsFilter.value
    const text = `${ws.pvc_name} ${ws.pv_name} ${ws.namespace} ${ws.node_name || ''} ${ws.app_display_name || ''} ${ws.app_id} ${ws.pool_name} ${ws.instance_name || ''} ${ws.instance_id || ''}`.toLocaleLowerCase()
    const textMatch = !q || text.includes(q)
    return stateMatch && textMatch
  })
})

function formatGiB(bytes: number | null | undefined): string {
  if (!valid(bytes) || bytes <= 0) return '—'
  const gib = bytes / 1024 ** 3
  return gib >= 10 ? `${Math.round(gib)} GiB` : `${gib.toFixed(1)} GiB`
}

function formatDiskUsedGB(bytes: number | null | undefined): string {
  if (!valid(bytes)) return '—'
  return `${Math.round(bytes / 1e9)} GB`
}

function formatDiskTotalTB(bytes: number | null | undefined): string {
  if (!valid(bytes)) return '—'
  return `${(bytes / 1e12).toFixed(1)} TB`
}

function formatDiskAvailTB(disk: Disk): string {
  if (valid(disk.avail_bytes)) return `${(disk.avail_bytes / 1e12).toFixed(2)} TB`
  const avail = Math.max(0, (disk.total_bytes || 0) - (disk.used_bytes || 0))
  return `${(avail / 1e12).toFixed(2)} TB`
}

function wsStateLabel(wsState: WorkspaceVolume['state']): string {
  switch (wsState) {
    case 'in_use':
      return '挂载运行中'
    case 'bound_stopped':
      return '已停止绑定'
    case 'retained':
      return '待释放 · 实例已销毁'
    default:
      return wsState
  }
}

function wsInstanceDisplay(ws: WorkspaceVolume): string {
  if (ws.instance_name) return ws.instance_name
  if (ws.state === 'retained') {
    return `${ws.app_display_name || ws.app_id} 历史实例（已销毁）`
  }
  return ws.instance_id || '已绑定实例'
}

// Release Workspace Dialog state
const releaseDialogRef = ref<HTMLDialogElement | null>(null)
const pendingReleaseWs = ref<WorkspaceVolume | null>(null)
const releaseInputName = ref('')
const releaseError = ref('')
const releasingWorkspace = ref(false)

function openReleaseDialog(ws: WorkspaceVolume) {
  pendingReleaseWs.value = ws
  releaseInputName.value = ''
  releaseError.value = ''
  releasingWorkspace.value = false
  const dlg = releaseDialogRef.value
  if (dlg) {
    if (typeof dlg.showModal === 'function' && !dlg.open) {
      dlg.showModal()
    } else {
      dlg.setAttribute('open', 'open')
    }
  }
}

function closeReleaseDialog() {
  const dlg = releaseDialogRef.value
  if (dlg) {
    if (typeof dlg.close === 'function' && dlg.open) {
      dlg.close()
    } else {
      dlg.removeAttribute('open')
    }
  }
  pendingReleaseWs.value = null
  releaseInputName.value = ''
  releaseError.value = ''
}

function makeUuidV4(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, c => {
    const r = (Math.random() * 16) | 0
    const v = c === 'x' ? r : (r & 0x3) | 0x8
    return v.toString(16)
  })
}

let activeSdk: AppSDK | null = null
let polling: ReturnType<typeof createPolling> | undefined
let observer: ResizeObserver | undefined
let stopTheme = () => {}
let stopLifecycle = () => {}
let stopNavigation = () => {}

function publishCounts() {
  const detail = {
    workspaces: workspacesState.value === 'ready' ? workspaces.value.length : null,
    tasks: dashboard.value ? workloads.value.length : null
  }
  try {
    window.dispatchEvent(new CustomEvent('studio:resources-counts', { detail }))
    if (window.parent && window.parent !== window) {
      window.parent.dispatchEvent(new CustomEvent('studio:resources-counts', { detail }))
    }
  } catch {
    /* ignore cross-window dispatch errors */
  }
}

async function fetchWorkspaces() {
  if (!activeSdk) return
  try {
    const res = await activeSdk.app.request({ path: '/api/v1/workspaces', method: 'GET' })
    if (res.status === 200 && isWorkspacesResponse(res.data)) {
      workspaces.value = res.data.workspaces
      workspacesState.value = 'ready'
      publishCounts()
    } else if (workspacesState.value === 'loading') {
      workspacesState.value = 'error'
    }
  } catch {
    if (workspacesState.value === 'loading') {
      workspacesState.value = 'error'
    }
  }
}

function showToast(msg: string) {
  toastMessage.value = msg
  setTimeout(() => {
    if (toastMessage.value === msg) toastMessage.value = ''
  }, 3500)
}

async function submitReleaseWorkspace() {
  const target = pendingReleaseWs.value
  if (!target || releasingWorkspace.value || !activeSdk) return
  if (releaseInputName.value.trim() !== target.pvc_name) return

  releasingWorkspace.value = true
  releaseError.value = ''
  try {
    const res = await activeSdk.app.request({
      path: '/api/v1/workspaces/release',
      method: 'POST',
      body: {
        idempotency_key: makeUuidV4(),
        namespace: target.namespace,
        pvc_name: target.pvc_name
      }
    })
    if (res.status !== 200) {
      const errData = (res.data && typeof res.data === 'object' ? res.data : {}) as {
        code?: string
        message?: string
      }
      if (errData.code === 'INSTANCE_NOT_DESTROYED') {
        releaseError.value = '关联实例尚未销毁，请先在应用控制台销毁实例后再释放工作卷。'
      } else if (errData.code === 'WORKSPACE_IN_USE') {
        releaseError.value = '该工作卷当前正被运行中的容器挂载占用，无法释放。'
      } else {
        releaseError.value = errData.message || '释放工作卷失败，请稍后重试。'
      }
      return
    }
    const releasedPvc = target.pvc_name
    const releasedPv = target.pv_name || `pv-${target.pvc_name}`
    closeReleaseDialog()
    await Promise.all([polling?.refresh(), fetchWorkspaces()])
    showToast(`工作卷声明 (PVC) ${releasedPvc} 与物理持久卷 (PV) ${releasedPv} 已彻底释放回收`)
  } catch {
    releaseError.value = '网络请求异常，请检查连接后重试。'
  } finally {
    releasingWorkspace.value = false
  }
}

function switchView(view: ResourceView) {
  currentView.value = view
  const nextPath = view === 'monitor' ? '/' : `/?view=${view}`
  try {
    void activeSdk?.navigation?.request?.({ target: 'app', path: nextPath })
  } catch {
    /* ignore if navigation.request is not granted */
  }
}

async function refresh() {
  refreshing.value = true
  try {
    await Promise.all([polling?.refresh(), fetchWorkspaces()])
  } finally {
    refreshing.value = false
  }
}

function cleanup() {
  polling?.dispose()
  observer?.disconnect()
  stopTheme()
  stopLifecycle()
  stopNavigation()
}

onMounted(() => {
  const props = hostProps()
  if (!props) {
    state.value = 'error'
    error.value = '请从 Studio 应用中心打开资源管理。'
    return
  }
  activeSdk = props.sdk
  document.body.dataset.theme = props.sdk.theme.get()
  stopTheme = props.sdk.theme.subscribe(next => {
    document.body.dataset.theme = next
  })
  if (props.sdk.navigation) {
    currentView.value = parseResourceView(props.sdk.navigation.get().path)
    stopNavigation = props.sdk.navigation.subscribe(route => {
      currentView.value = parseResourceView(route.path)
    })
  } else {
    currentView.value = parseResourceView(window.location.search)
  }
  stopLifecycle = props.sdk.lifecycle.subscribe(event => {
    if (event === 'dispose') cleanup()
    if (event === 'active') void fetchWorkspaces()
  })
  polling = createPolling(props.sdk, result => {
    state.value = result.state
    dashboard.value = result.data
    error.value = result.message
    if (result.state === 'ready') {
      publishCounts()
      void fetchWorkspaces()
    }
  })
  const root = document.getElementById('app')!
  observer = new ResizeObserver(() => props.host.resize(Math.ceil(root.getBoundingClientRect().height)))
  observer.observe(root)
  props.host.ready()
  void refresh()
})

onBeforeUnmount(cleanup)
</script>

<template>
  <main class="main" id="mainContent" data-embedded="true">
    <div class="workspace">
      <header class="heading">
        <div class="heading-left">
          <h1 id="pageTitle">{{ viewMeta[currentView].title }}</h1>
          <p id="pageSubtitle">{{ viewMeta[currentView].subtitle }}</p>
        </div>
        <div class="heading-actions">
          <div class="workload-tabs" role="tablist" aria-label="资源管理子视图">
            <button type="button"
                    class="workload-tab-btn"
                    :class="{ active: currentView === 'monitor' }"
                    data-res-view="monitor"
                    @click="switchView('monitor')">资源监控</button>
            <button type="button"
                    class="workload-tab-btn"
                    :class="{ active: currentView === 'storage' }"
                    data-res-view="storage"
                    @click="switchView('storage')">存储管理</button>
            <button type="button"
                    class="workload-tab-btn"
                    :class="{ active: currentView === 'tasks' }"
                    data-res-view="tasks"
                    @click="switchView('tasks')">任务监控</button>
          </div>
          <button class="refresh"
                  id="refreshBtn"
                  :disabled="refreshing || state === 'loading'"
                  :aria-label="viewMeta[currentView].refreshLabel"
                  @click="refresh">
            {{ refreshing ? '刷新中…' : viewMeta[currentView].refreshLabel }}
          </button>
        </div>
      </header>

      <p v-if="toastMessage" class="notice" role="status">{{ toastMessage }}</p>
      <p v-if="state === 'loading'" class="notice" role="status">正在读取资源观测…</p>
      <p v-else-if="state === 'error'" class="notice error" role="alert">{{ error }}</p>

      <template v-else-if="dashboard">
        <p v-if="hardwareNotice" class="notice" role="status">{{ hardwareNotice }}</p>
        <p class="observation">观测时间 {{ new Date(dashboard.observed_at).toLocaleString('zh-CN') }}</p>

        <!-- Sub-view 1: 资源监控 -->
        <div v-if="currentView === 'monitor'" id="monitorView" class="resource-dashboard">
          <section class="res-metric-grid" aria-label="资源总览">
            <article class="res-metric-card" id="kpiGpu">
              <div class="res-metric-label">GPU 集群算力</div>
              <div class="res-metric-val" id="resGpuCount">{{ numeric(summary.total_gpus, 0) }} <small>张 GPU</small></div>
              <div class="res-metric-sub" id="resGpuSub">显存 {{ scaled(summary.used_vram_mb, 1024) }} / {{ scaled(summary.total_vram_mb, 1024) }} GiB</div>
              <div class="res-metric-meter" :aria-label="'显存占比 ' + numeric(ratio(summary.used_vram_mb, summary.total_vram_mb))">
                <div class="res-metric-meter-fill" id="resGpuMeter" :style="meter(ratio(summary.used_vram_mb, summary.total_vram_mb))"></div>
              </div>
            </article>
            <article class="res-metric-card" id="kpiCpu">
              <div class="res-metric-label">CPU 运算负载</div>
              <div class="res-metric-val" id="resCpuVal">{{ numeric(summary.host_cpu_utilization) }} <small>%</small></div>
              <div class="res-metric-sub" id="resCpuSub">{{ numeric(summary.host_cpu_cores, 0) }} vCPUs · 1m 负载 {{ numeric(summary.host_cpu_load1, 2) }}</div>
              <div class="res-metric-meter">
                <div class="res-metric-meter-fill" id="resCpuMeter" :style="meter(summary.host_cpu_utilization)"></div>
              </div>
            </article>
            <article class="res-metric-card" id="kpiRam">
              <div class="res-metric-label">系统内存</div>
              <div class="res-metric-val" id="resRamVal">{{ numeric(summary.host_mem_used_gb) }} <small>/ {{ numeric(summary.host_mem_total_gb) }} GiB</small></div>
              <div class="res-metric-sub" id="resRamSub">使用率 {{ numeric(summary.host_mem_used_percent) }}%</div>
              <div class="res-metric-meter">
                <div class="res-metric-meter-fill" id="resRamMeter" :style="meter(summary.host_mem_used_percent)"></div>
              </div>
            </article>
            <article class="res-metric-card" id="kpiStorage">
              <div class="res-metric-label">存储水位</div>
              <div class="res-metric-val" id="resStorageVal">{{ numeric(summary.storage_used_tb, 2) }} <small>/ {{ numeric(summary.storage_total_tb, 2) }} TB</small></div>
              <div class="res-metric-sub" id="resStorageSub">使用率 {{ numeric(summary.storage_used_percent) }}%</div>
              <div class="res-metric-meter">
                <div class="res-metric-meter-fill" id="resStorageMeter" :style="meter(summary.storage_used_percent)"></div>
              </div>
            </article>
          </section>

          <section class="res-section">
            <div class="res-section-title">
              <div class="res-section-title-left">
                <span id="resTopologyTitle">GPU 拓扑与实时遥测</span>
                <span class="res-section-badge">{{ numeric(summary.total_gpus, 0) }} 张可观测设备</span>
              </div>
            </div>
            <p v-if="!gpus.length" class="empty">
              {{ dashboard.hardware_state === 'fresh' && dashboard.gpus ? '当前没有 GPU 设备。' : 'GPU 观测暂不可用。' }}
            </p>
            <div class="gpu-grid">
              <article v-for="g in gpus" :key="g.uuid || `${g.device}:${g.index}`" class="gpu-card" :data-gpu="g.index">
                <div class="gpu-card-head">
                  <span class="gpu-id">GPU {{ g.index }}</span>
                  <span class="gpu-name">{{ g.model_name || '型号未提供' }} · {{ scaled(g.total_vram_mb, 1024) }} GiB</span>
                  <span class="gpu-state" :class="valid(g.utilization) && g.utilization > 5 ? 'active' : 'standby'">
                    {{ valid(g.utilization) ? (g.utilization > 5 ? '运行中' : '低负载') : '利用率未知' }}
                  </span>
                </div>
                <div class="gpu-bar-wrap">
                  <div class="gpu-bar-fill" :style="meter(ratio(g.used_vram_mb, g.total_vram_mb))"></div>
                </div>
                <div class="gpu-stat-row">
                  <span class="gpu-stat-vram">显存 <strong>{{ scaled(g.used_vram_mb, 1024) }}</strong> / {{ scaled(g.total_vram_mb, 1024) }} GiB</span>
                  <span class="gpu-stat-telemetry">{{ numeric(g.temperature_c, 0) }}°C · {{ numeric(g.power_watts, 0) }}W</span>
                </div>
                <div class="gpu-workload">
                  <span>利用率 {{ numeric(g.utilization) }}% · 剩余显存 {{ scaled(g.free_vram_mb, 1024) }} GiB</span>
                </div>
              </article>
            </div>
          </section>

          <section class="res-section">
            <div class="res-section-title">
              <div class="res-section-title-left">
                <span>主机运算与系统内存</span>
                <span class="res-section-badge">{{ numeric(node?.cpu_cores, 0) }} vCPUs · {{ scaled(node?.mem_total_bytes, 1024 ** 3) }} GiB</span>
              </div>
            </div>
            <div class="res-host-grid">
              <article class="host-detail-card" id="cpuDetailCard">
                <div class="host-detail-head">
                  <div class="host-detail-title-wrap"><div class="host-detail-name">CPU 处理器运算单元</div></div>
                  <div class="host-detail-stat" id="cpuLivePct">{{ numeric(node?.cpu_utilization_percent) }}% <small>利用率</small></div>
                </div>
                <div class="res-metric-meter host-meter">
                  <div class="res-metric-meter-fill" id="cpuLiveBar" :style="meter(node?.cpu_utilization_percent)"></div>
                </div>
                <div class="host-meta-grid">
                  <div class="host-meta-item">
                    <span class="host-meta-lbl">计算规格</span>
                    <strong class="host-meta-val" id="cpuCoresVal">{{ numeric(node?.cpu_cores, 0) }} vCPUs</strong>
                  </div>
                  <div class="host-meta-item">
                    <span class="host-meta-lbl">系统负载（1m / 5m / 15m）</span>
                    <strong class="host-meta-val" id="cpuLoadVal">{{ numeric(node?.cpu_load1, 2) }} / {{ numeric(node?.cpu_load5, 2) }} / {{ numeric(node?.cpu_load15, 2) }}</strong>
                  </div>
                </div>
              </article>
              <article class="host-detail-card" id="ramDetailCard">
                <div class="host-detail-head">
                  <div class="host-detail-title-wrap"><div class="host-detail-name">系统运行内存池</div></div>
                  <div class="host-detail-stat" id="ramLiveVal">{{ scaled(node?.mem_used_bytes, 1024 ** 3) }} <small>/ {{ scaled(node?.mem_total_bytes, 1024 ** 3) }} GiB</small></div>
                </div>
                <div class="res-metric-meter host-meter">
                  <div class="res-metric-meter-fill" id="ramLiveBar" :style="meter(node?.mem_used_percent)"></div>
                </div>
                <div class="host-meta-grid">
                  <div class="host-meta-item"><span class="host-meta-lbl">活跃占用</span><strong class="host-meta-val" id="ramUsedVal">{{ formatResource(node?.mem_used_bytes, 'bytes') }}</strong></div>
                  <div class="host-meta-item"><span class="host-meta-lbl">文件系统缓存</span><strong class="host-meta-val" id="ramCacheVal">{{ formatResource(node?.mem_cached_bytes, 'bytes') }}</strong></div>
                  <div class="host-meta-item"><span class="host-meta-lbl">剩余可用</span><strong class="host-meta-val" id="ramAvailVal">{{ formatResource(node?.mem_available_bytes, 'bytes') }}</strong></div>
                  <div class="host-meta-item"><span class="host-meta-lbl">缓冲区</span><strong class="host-meta-val">{{ formatResource(node?.mem_buffers_bytes, 'bytes') }}</strong></div>
                </div>
              </article>
            </div>
          </section>
        </div>

        <!-- Sub-view 2: 存储管理 -->
        <div v-else-if="currentView === 'storage'" id="storageView" class="resource-dashboard">
          <section class="res-section">
            <div class="res-section-title">
              <div class="res-section-title-left">
                <span>节点物理存储与挂载点水位</span>
                <span class="res-section-badge">NVMe PCIe 4.0/5.0 直通</span>
              </div>
              <span class="eyebrow">LOCAL FAST-TIER &amp; S3 SYNC</span>
            </div>
            <div class="res-storage-cards">
              <!-- / Root Partition -->
              <article class="storage-card" id="storageRootCard">
                <div class="storage-head">
                  <div class="storage-mount-wrap">
                    <span class="storage-mount">/</span>
                    <span class="storage-badge">宿主系统盘 · {{ rootDisk.fstype || 'Ext4' }}</span>
                  </div>
                  <span class="storage-val" id="storageRootVal">
                    {{ formatDiskUsedGB(rootDisk.used_bytes) }} <small>/ {{ formatDiskTotalTB(rootDisk.total_bytes) }} ({{ numeric(rootDisk.used_percent, 0) }}%)</small>
                  </span>
                </div>
                <div class="storage-bar">
                  <div class="storage-fill" id="storageRootBar" :style="meter(rootDisk.used_percent)"></div>
                </div>
                <div class="storage-meta">
                  <span>承载：Containerd 容器镜像层 + Pod 临时存储</span>
                  <span class="storage-status-ok"><i class="gpu-dot"></i>可用余量：{{ formatDiskAvailTB(rootDisk) }}</span>
                </div>
              </article>

              <!-- /data Partition -->
              <article class="storage-card" id="storageDataCard">
                <div class="storage-head">
                  <div class="storage-mount-wrap">
                    <span class="storage-mount">/data</span>
                    <span class="storage-badge">AI 模型与制品高速卷 · {{ dataDisk.fstype || 'Ext4' }}</span>
                  </div>
                  <span class="storage-val" id="storageDataVal">
                    {{ formatDiskUsedGB(dataDisk.used_bytes) }} <small>/ {{ formatDiskTotalTB(dataDisk.total_bytes) }} ({{ numeric(dataDisk.used_percent, 0) }}%)</small>
                  </span>
                </div>
                <div class="storage-bar">
                  <div class="storage-fill" id="storageDataBar" :style="meter(dataDisk.used_percent)"></div>
                </div>
                <div class="storage-meta">
                  <span>承载：/data/models (模型缓存) + /data/artifacts (制品库)</span>
                  <span class="storage-status-ok"><i class="gpu-dot"></i>可用余量：{{ formatDiskAvailTB(dataDisk) }}</span>
                </div>
              </article>
            </div>
          </section>

          <!-- 应用工作卷池与绑定台账 -->
          <div class="ws-vol-panel" id="workspaceVolumesPanel">
            <div class="ws-vol-head">
              <div class="ws-vol-title-group">
                <span class="ws-vol-title">应用工作卷池与绑定台账</span>
                <span class="res-section-badge">Station 全局统一纳管</span>
                <span class="res-section-badge"
                      :class="wsCounts.retained > 0 ? 'amber' : 'highlight'"
                      id="wsRetainedBadge">
                  {{ wsCounts.retained > 0 ? `${wsCounts.retained} 个已销毁实例工作卷待释放` : '无待释放残留工作卷' }}
                </span>
              </div>
              <span class="ws-vol-sub">实例销毁后工作卷保留数据；确认无需保留后点击释放，将清空 PV 盘面并删除 PVC 声明，记录从表中移除。</span>
            </div>

            <div class="ws-vol-toolbar">
              <div class="workload-tabs" id="wsVolTabs" role="tablist" aria-label="工作卷状态筛选">
                <button class="workload-tab-btn"
                        :class="{ active: wsFilter === 'all' }"
                        type="button"
                        data-ws-filter="all"
                        @click="wsFilter = 'all'">
                  全部工作卷 (<span id="wsCountAll">{{ wsCounts.all }}</span>)
                </button>
                <button class="workload-tab-btn"
                        :class="{ active: wsFilter === 'in_use' }"
                        type="button"
                        data-ws-filter="in_use"
                        @click="wsFilter = 'in_use'">
                  挂载运行中 (<span id="wsCountInUse">{{ wsCounts.in_use }}</span>)
                </button>
                <button class="workload-tab-btn"
                        :class="{ active: wsFilter === 'bound_stopped' }"
                        type="button"
                        data-ws-filter="bound_stopped"
                        @click="wsFilter = 'bound_stopped'">
                  已停止绑定 (<span id="wsCountStopped">{{ wsCounts.bound_stopped }}</span>)
                </button>
                <button class="workload-tab-btn"
                        :class="{ active: wsFilter === 'retained' }"
                        type="button"
                        data-ws-filter="retained"
                        @click="wsFilter = 'retained'">
                  待释放 (<span id="wsCountRetained">{{ wsCounts.retained }}</span>)
                </button>
              </div>
              <div class="workload-search">
                <input v-model="wsSearchQuery"
                       type="text"
                       id="wsFilterInput"
                       aria-label="过滤工作卷"
                       placeholder="过滤 PVC 声明、PV 持久卷、应用或实例...">
              </div>
            </div>

            <div class="ws-vol-table-wrap">
              <table class="ws-vol-table" aria-label="应用工作卷列表">
                <thead>
                  <tr>
                    <th>工作卷声明 (PVC)</th>
                    <th>物理持久卷 (PV)</th>
                    <th>所属应用 / 卷池</th>
                    <th>关联实例</th>
                    <th>状态</th>
                    <th>预留容量</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody id="wsVolTableBody">
                  <tr v-if="workspacesState === 'loading'">
                    <td colspan="7" style="text-align: center; color: var(--muted); padding: 24px;">正在读取应用工作卷池台账…</td>
                  </tr>
                  <tr v-else-if="workspacesState === 'error'">
                    <td colspan="7" style="text-align: center; color: var(--muted); padding: 24px;">工作卷池台账暂不可用，请点击右上角刷新重试。</td>
                  </tr>
                  <tr v-else-if="!filteredWorkspaces.length">
                    <td colspan="7" style="text-align: center; color: var(--muted); padding: 24px;">没有符合筛选条件的工作卷记录。</td>
                  </tr>
                  <tr v-else
                      v-for="ws in filteredWorkspaces"
                      :key="ws.workspace_id"
                      :data-ws-state="ws.state"
                      :data-pvc="ws.pvc_name"
                      :data-pv="ws.pv_name || ('pv-' + ws.pvc_name)">
                    <td>
                      <div class="ws-cell-main ws-cell-mono">{{ ws.pvc_name }}</div>
                      <div class="ws-cell-sub">NS: {{ ws.namespace }}</div>
                    </td>
                    <td>
                      <div class="ws-cell-main ws-cell-mono">{{ ws.pv_name || ('pv-' + ws.pvc_name) }}</div>
                      <div class="ws-cell-sub">Node: {{ ws.node_name || 'verdentflare-5090' }}</div>
                    </td>
                    <td>
                      <div class="ws-cell-main">{{ ws.app_display_name || ws.app_id }}</div>
                      <div class="ws-cell-sub">Pool: {{ ws.pool_name }}</div>
                    </td>
                    <td class="ws-instance-cell">
                      <div class="ws-cell-main">{{ wsInstanceDisplay(ws) }}</div>
                      <div class="ws-cell-sub">ID: {{ ws.instance_id || '—（实例已销毁）' }}</div>
                    </td>
                    <td class="ws-state-cell">
                      <span class="ws-status-pill" :class="ws.state"><i class="dot"></i>{{ wsStateLabel(ws.state) }}</span>
                    </td>
                    <td>
                      <div class="ws-cell-main">{{ formatGiB(ws.storage_bytes) }}</div>
                      <div class="ws-cell-sub">{{ ws.storage_class || 'local-nvme' }} · {{ ws.access_mode === 'ReadWriteOnce' ? 'RWO' : (ws.access_mode || 'RWO') }}</div>
                    </td>
                    <td class="ws-action-cell">
                      <button v-if="ws.state === 'retained' && (ws.allowed_actions || []).includes('release')"
                              type="button"
                              class="ws-btn-release"
                              :data-release-pvc="ws.pvc_name"
                              @click="openReleaseDialog(ws)">
                        释放工作卷
                      </button>
                      <button v-else
                              type="button"
                              class="ws-btn-release"
                              disabled
                              :title="ws.state === 'in_use' ? '实例运行中，请先停止并销毁实例' : '请先在应用控制台销毁关联实例，再释放工作卷'">
                        释放工作卷
                      </button>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </div>

        <!-- Sub-view 3: 任务监控 -->
        <div v-else-if="currentView === 'tasks'" id="tasksView" class="resource-dashboard">
          <section class="res-section" id="workloadSection">
            <div class="res-section-title">
              <div class="res-section-title-left">
                <span>已部署工作负载与算力配额对账</span>
                <span class="res-section-badge highlight">{{ dashboard.workloads_summary.total_pods }} 个活跃容器组</span>
                <span class="res-section-badge">配置请求与真实采样分别呈现</span>
              </div>
              <span class="eyebrow">CONTAINER LEVEL QUOTA &amp; ACTUAL IN-USE</span>
            </div>
            <div class="workloads-container">
              <div class="quota-summary-strip">
                <div class="quota-item"><span class="quota-item-lbl">容器组</span><div class="quota-item-val">{{ dashboard.workloads_summary.total_pods }}</div></div>
                <div class="quota-item"><span class="quota-item-lbl">活跃 Pod GPU 请求（非实际分配）</span><div class="quota-item-val">{{ dashboard.workloads_summary.total_gpu_requested }}</div></div>
                <div class="quota-item"><span class="quota-item-lbl">活跃 Pod CPU 请求</span><div class="quota-item-val">{{ formatResource(dashboard.workloads_summary.total_cpu_requested) }}</div></div>
                <div class="quota-item"><span class="quota-item-lbl">活跃 Pod 内存请求</span><div class="quota-item-val">{{ formatResource(dashboard.workloads_summary.total_memory_requested, 'bytes') }}</div></div>
              </div>
              <div class="workload-filters">
                <div class="workload-tabs">
                  <button class="workload-tab-btn" :class="{ active: currentFilter === 'all' }" data-filter="all" @click="currentFilter = 'all'">全部工作负载 ({{ workloadCounts.all }})</button>
                  <button class="workload-tab-btn" :class="{ active: currentFilter === 'gpu' }" data-filter="gpu" @click="currentFilter = 'gpu'">配置 GPU ({{ workloadCounts.gpu }})</button>
                  <button class="workload-tab-btn" :class="{ active: currentFilter === 'infra' }" data-filter="infra" @click="currentFilter = 'infra'">未配置 GPU ({{ workloadCounts.infra }})</button>
                </div>
                <div class="workload-search">
                  <input v-model="searchQuery" id="filterInput" type="search" aria-label="搜索工作负载" placeholder="搜索应用或命名空间">
                </div>
              </div>
              <p v-if="!workloads.length" class="empty">暂无工作负载。</p>
              <p v-else-if="!filteredWorkloads.length" class="empty">没有符合筛选条件的工作负载。</p>
              <div class="workload-list" id="workloadList">
                <article v-for="wl in filteredWorkloads" :key="wl.pod_uid" class="wl-card" :class="wl.type === 'gpu' ? 'has-gpu' : 'cpu-only'" :data-type="wl.type" :data-name="wl.name">
                  <div class="wl-head">
                    <div class="wl-title-box">
                      <div class="wl-title-row">
                        <span class="wl-name">{{ wl.display_name || wl.name }}</span>
                        <span class="wl-namespace">{{ wl.namespace }}</span>
                      </div>
                      <span class="wl-pod-id">{{ wl.name }}</span>
                    </div>
                    <div class="wl-meta-right">
                      <span class="wl-uptime">{{ wl.age }}</span>
                      <span class="wl-status-tag running">{{ wl.status }}</span>
                    </div>
                  </div>
                  <div v-for="container in wl.containers" :key="container.name">
                    <div class="wl-foot"><span>{{ container.name }} · {{ container.ready ? '就绪' : '未就绪' }} · 重启 {{ container.restarts }} 次</span></div>
                    <div class="wl-metrics-grid">
                      <div v-for="kind in (['cpu', 'memory'] as const)" :key="kind" class="wl-metric-cell">
                        <div class="wl-cell-title">{{ kind === 'cpu' ? 'CPU 使用' : '内存工作集' }}</div>
                        <div class="wl-actual-val">{{ resourceValue(container[kind], kind === 'cpu' ? 'cores' : 'bytes') }}</div>
                        <div class="wl-metric-footnote">请求 {{ formatResource(container[kind].request, kind === 'cpu' ? 'cores' : 'bytes') }} · 上限 {{ formatResource(container[kind].limit, kind === 'cpu' ? 'cores' : 'bytes') }}</div>
                        <div class="wl-metric-footnote">{{ resourceState(container[kind]) === 'fresh' ? '采样 ' + new Date(container[kind].sampled_at!).toLocaleString('zh-CN') : resourceState(container[kind]) === 'stale' ? '采样已过期' : '未采集' }}</div>
                      </div>
                      <div class="wl-metric-cell">
                        <div class="wl-cell-title">GPU 配置请求</div>
                        <div class="wl-actual-val">{{ container.gpu_request ?? '—' }}</div>
                        <div class="wl-metric-footnote">实际设备分配与显存用量待绑定确认</div>
                      </div>
                    </div>
                  </div>
                </article>
              </div>
            </div>
          </section>
        </div>
      </template>
    </div>

    <!-- 释放工作卷二次确认弹窗 -->
    <dialog ref="releaseDialogRef"
            id="release-workspace-dialog"
            class="ws-dialog"
            aria-labelledby="release-workspace-title">
      <form class="ws-dialog-card"
            method="dialog"
            id="release-workspace-form"
            @submit.prevent="submitReleaseWorkspace">
        <div class="ws-dialog-head">
          <div>
            <span class="eyebrow" style="color: #c43d2f;">WORKSPACE VOLUME RELEASE</span>
            <h3 id="release-workspace-title" class="ws-dialog-title">确认释放工作卷</h3>
          </div>
          <button type="button"
                  class="ws-btn-cancel"
                  id="release-workspace-cancel-top"
                  :disabled="releasingWorkspace"
                  @click="closeReleaseDialog">取消</button>
        </div>
        <div class="ws-dialog-body">
          <p class="ws-dialog-desc">
            释放后将清空物理持久卷 (PV) <code><span id="release-workspace-pv-code">{{ pendingReleaseWs?.pv_name || (pendingReleaseWs ? 'pv-' + pendingReleaseWs.pvc_name : '') }}</span></code>
            的残留数据，删除工作卷声明 (PVC) <code><span id="release-workspace-target-code">{{ pendingReleaseWs?.pvc_name || '' }}</span></code>
            并解除其与底层 PV 的绑定，使其回到空闲可用池（当前记录将从列表移除）。
          </p>
          <div class="ws-dialog-field">
            <span>请输入工作卷 PVC 名称 <code><span id="release-workspace-expected">{{ pendingReleaseWs?.pvc_name || '' }}</span></code> 以确认释放</span>
            <input v-model="releaseInputName"
                   id="release-workspace-name"
                   type="text"
                   autocomplete="off"
                   :disabled="releasingWorkspace"
                   placeholder="输入工作卷 PVC 名称确认释放">
          </div>
          <div v-if="releaseError"
               class="ws-dialog-error"
               id="release-workspace-error"
               role="alert">{{ releaseError }}</div>
        </div>
        <div class="ws-dialog-actions">
          <button type="button"
                  class="ws-btn-cancel"
                  id="release-workspace-cancel"
                  :disabled="releasingWorkspace"
                  @click="closeReleaseDialog">保留绑定</button>
          <button type="submit"
                  class="ws-btn-release"
                  id="release-workspace-confirm"
                  :disabled="releasingWorkspace || releaseInputName.trim() !== (pendingReleaseWs?.pvc_name || '')">
            {{ releasingWorkspace ? '正在释放…' : '确认释放工作卷' }}
          </button>
        </div>
      </form>
    </dialog>
  </main>
</template>

<style scoped>
.main{margin:0;padding:28px 36px 48px;min-width:0}
.workspace{width:100%}
.heading{display:flex;align-items:flex-end;justify-content:space-between;gap:20px;margin-bottom:20px;flex-wrap:wrap}
.heading h1{font-size:30px;font-weight:600;letter-spacing:-.6px}
.heading p{color:var(--muted);margin-top:6px;font-size:13px}
.heading-actions{display:flex;align-items:center;gap:10px;flex-wrap:wrap}
.refresh{padding:8px 16px;border:1px solid var(--line);border-radius:9px;background:var(--shell-panel)}
.notice{padding:16px;border:1px solid var(--line);border-radius:9px;margin-bottom:16px}
.error{color:var(--danger)}
.observation{color:var(--muted);font-size:12px;margin-bottom:16px}
.empty{padding:14px;color:var(--muted)}
.resource-dashboard{overflow:visible}
.storage-meta{flex-wrap:wrap}
.wl-pod-id{overflow-wrap:anywhere}

.res-section-badge.amber {
  background: rgba(180, 105, 14, 0.12);
  color: #b4690e;
  border-color: rgba(180, 105, 14, 0.3);
}

.ws-vol-panel {
  background: var(--shell-panel, var(--panel));
  border: 1px solid var(--line);
  border-radius: 16px;
  overflow: hidden;
  box-shadow: 0 4px 18px rgba(21, 35, 30, 0.02);
}

.ws-vol-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 18px 24px;
  border-bottom: 1px solid var(--line);
  flex-wrap: wrap;
}

.ws-vol-title-group {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.ws-vol-title {
  font-size: 15px;
  font-weight: 650;
  color: var(--ink);
  letter-spacing: -0.2px;
}

.ws-vol-sub {
  font-size: 12px;
  color: var(--muted);
}

.ws-vol-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 24px;
  background: var(--shell-button);
  border-bottom: 1px solid var(--line);
  flex-wrap: wrap;
}

.ws-vol-table-wrap {
  width: 100%;
  overflow-x: auto;
  background: var(--panel);
}

.ws-vol-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12.5px;
  text-align: left;
}

.ws-vol-table th {
  padding: 11px 24px;
  font-size: 11px;
  font-weight: 600;
  color: var(--muted);
  border-bottom: 1px solid var(--line);
  white-space: nowrap;
}

.ws-vol-table th:last-child,
.ws-vol-table td:last-child {
  text-align: right;
}

.ws-vol-table td {
  padding: 13px 24px;
  border-bottom: 1px solid var(--line);
  vertical-align: middle;
  color: var(--ink);
}

.ws-vol-table tbody tr:last-child td {
  border-bottom: 0;
}

.ws-vol-table tbody tr:hover {
  background: rgba(8, 126, 96, 0.03);
}

.ws-cell-main {
  font-weight: 600;
  color: var(--ink);
  display: flex;
  align-items: center;
  gap: 6px;
}

.ws-cell-mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 12.5px;
  font-weight: 700;
}

.ws-cell-sub {
  font-size: 11px;
  color: var(--muted);
  margin-top: 2px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}

.ws-status-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 3px 9px;
  border-radius: 9999px;
  font-size: 11px;
  font-weight: 600;
  white-space: nowrap;
}

.ws-status-pill .dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
}

.ws-status-pill.in_use {
  background: rgba(8, 126, 96, 0.1);
  color: var(--accent);
  border: 1px solid rgba(8, 126, 96, 0.28);
}

.ws-status-pill.in_use .dot {
  background: var(--accent);
}

.ws-status-pill.bound_stopped {
  background: var(--shell-button);
  color: var(--muted);
  border: 1px solid var(--line);
}

.ws-status-pill.bound_stopped .dot {
  background: var(--muted);
}

.ws-status-pill.retained {
  background: rgba(180, 105, 14, 0.12);
  color: #b4690e;
  border: 1px solid rgba(180, 105, 14, 0.32);
}

.ws-status-pill.retained .dot {
  background: #b4690e;
}

.ws-status-pill.available {
  background: rgba(45, 108, 223, 0.1);
  color: #2d6cdf;
  border: 1px solid rgba(45, 108, 223, 0.26);
}

.ws-status-pill.available .dot {
  background: #2d6cdf;
}

.ws-btn-release {
  padding: 5px 11px;
  border-radius: 6px;
  font-size: 12px;
  font-weight: 600;
  border: 1px solid rgba(196, 61, 47, 0.35);
  background: rgba(196, 61, 47, 0.08);
  color: #c43d2f;
  cursor: pointer;
  transition: all 0.15s ease;
}

.ws-btn-release:hover:not(:disabled) {
  background: rgba(196, 61, 47, 0.15);
  border-color: #c43d2f;
}

.ws-btn-release:disabled {
  opacity: 0.42;
  cursor: not-allowed;
  border-color: var(--line);
  background: var(--shell-button);
  color: var(--muted);
}

.ws-dialog {
  border: 1px solid var(--line);
  border-radius: 14px;
  padding: 22px 24px;
  background: var(--panel);
  color: var(--ink);
  width: min(460px, 92vw);
  box-shadow: 0 18px 48px rgba(0, 0, 0, 0.18);
}

.ws-dialog::backdrop {
  background: rgba(15, 23, 20, 0.42);
  backdrop-filter: blur(2px);
}

.ws-dialog-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
}

.ws-dialog-title {
  font-size: 16px;
  font-weight: 700;
  margin: 4px 0 0;
}

.ws-dialog-desc {
  font-size: 12.5px;
  color: var(--muted);
  line-height: 1.55;
  margin: 0 0 14px;
}

.ws-dialog-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 12px;
  font-weight: 600;
  margin-bottom: 16px;
}

.ws-dialog-field input {
  padding: 8px 11px;
  border-radius: 8px;
  border: 1px solid var(--line);
  background: var(--bg);
  color: var(--ink);
  font-size: 13px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  outline: none;
}

.ws-dialog-field input:focus {
  border-color: var(--accent);
}

.ws-dialog-error {
  margin-bottom: 14px;
  padding: 8px 11px;
  border-radius: 8px;
  background: rgba(196, 61, 47, 0.08);
  border: 1px solid rgba(196, 61, 47, 0.3);
  color: #c43d2f;
  font-size: 12px;
}

.ws-dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.ws-btn-cancel {
  padding: 6px 14px;
  border-radius: 7px;
  border: 1px solid var(--line);
  background: var(--shell-button);
  color: var(--ink);
  font-size: 12.5px;
  font-weight: 600;
  cursor: pointer;
}

@media(max-width:700px){
  .main{padding:20px 16px}
  .heading{align-items:flex-start}
  .heading h1{font-size:26px}
  .wl-metrics-grid{grid-template-columns:1fr}
}
</style>
