<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { hostProps } from './sdk'
import { createPolling } from './polling'
import { formatResource, meter, numeric, ratio, resourceState, resourceValue, scaled, valid } from './model'
import type { Dashboard } from './model'

const dashboard = ref<Dashboard | null>(null)
const state = ref<'loading' | 'ready' | 'error'>('loading')
const error = ref('')
const currentFilter = ref<'all' | 'gpu' | 'infra'>('all')
const searchQuery = ref('')
const refreshing = ref(false)
const summary = computed(() => dashboard.value?.summary ?? {})
const node = computed(() => dashboard.value?.node)
const gpus = computed(() => dashboard.value?.gpus ?? [])
const workloads = computed(() => dashboard.value?.workloads ?? [])
const hardwareNotice = computed(() => dashboard.value?.hardware_state === 'stale' ? '硬件采样已过期，等待新观测。' : dashboard.value?.hardware_state === 'unavailable' ? '硬件观测暂不可用。' : '')
const filteredWorkloads = computed(() => {
  const q = searchQuery.value.trim().toLocaleLowerCase()
  return workloads.value.filter(w => (currentFilter.value === 'all' || w.type === currentFilter.value) && `${w.name} ${w.display_name} ${w.namespace}`.toLocaleLowerCase().includes(q))
})
let polling: ReturnType<typeof createPolling> | undefined
let observer: ResizeObserver | undefined
let stopTheme = () => {}
let stopLifecycle = () => {}
async function refresh() { refreshing.value = true; try { await polling?.refresh() } finally { refreshing.value = false } }
function cleanup() { polling?.dispose(); observer?.disconnect(); stopTheme(); stopLifecycle() }
onMounted(() => {
  const props = hostProps()
  if (!props) { state.value = 'error'; error.value = '请从 Studio 应用中心打开资源管理。'; return }
  document.body.dataset.theme = props.sdk.theme.get()
  stopTheme = props.sdk.theme.subscribe(next => { document.body.dataset.theme = next })
  stopLifecycle = props.sdk.lifecycle.subscribe(event => { if (event === 'dispose') cleanup() })
  polling = createPolling(props.sdk, result => { state.value = result.state; dashboard.value = result.data; error.value = result.message })
  const root = document.getElementById('app')!
  observer = new ResizeObserver(() => props.host.resize(Math.ceil(root.getBoundingClientRect().height)))
  observer.observe(root)
  props.host.ready()
  void refresh()
})
onBeforeUnmount(cleanup)
</script>

<template>
  <main class="main" data-embedded="true">
    <div class="workspace">
      <header class="heading">
        <div class="heading-left"><h1>资源管理</h1><p>Station 节点算力、存储水位与已部署工作负载配额／实况对账。</p></div>
        <button class="refresh" :disabled="refreshing || state === 'loading'" @click="refresh" aria-label="刷新遥测数据">{{ refreshing ? '刷新中…' : '刷新' }}</button>
      </header>
      <p v-if="state === 'loading'" class="notice" role="status">正在读取资源观测…</p>
      <p v-else-if="state === 'error'" class="notice error" role="alert">{{ error }}</p>
      <template v-else-if="dashboard">
        <p v-if="hardwareNotice" class="notice" role="status">{{ hardwareNotice }}</p>
        <p class="observation">观测时间 {{ new Date(dashboard.observed_at).toLocaleString('zh-CN') }}</p>
        <div class="resource-dashboard">
          <section class="res-metric-grid" aria-label="资源总览">
            <article class="res-metric-card"><div class="res-metric-label">GPU 集群算力</div><div class="res-metric-val">{{ numeric(summary.total_gpus, 0) }} <small>张 GPU</small></div><div class="res-metric-sub">显存 {{ scaled(summary.used_vram_mb, 1024) }} / {{ scaled(summary.total_vram_mb, 1024) }} GiB</div><div class="res-metric-meter" :aria-label="'显存占比 ' + numeric(ratio(summary.used_vram_mb, summary.total_vram_mb))"><div class="res-metric-meter-fill" :style="meter(ratio(summary.used_vram_mb, summary.total_vram_mb))"></div></div></article>
            <article class="res-metric-card"><div class="res-metric-label">CPU 运算负载</div><div class="res-metric-val">{{ numeric(summary.host_cpu_utilization) }} <small>%</small></div><div class="res-metric-sub">{{ numeric(summary.host_cpu_cores, 0) }} vCPUs · 1m 负载 {{ numeric(summary.host_cpu_load1, 2) }}</div><div class="res-metric-meter"><div class="res-metric-meter-fill" :style="meter(summary.host_cpu_utilization)"></div></div></article>
            <article class="res-metric-card"><div class="res-metric-label">系统内存</div><div class="res-metric-val">{{ numeric(summary.host_mem_used_gb) }} <small>/ {{ numeric(summary.host_mem_total_gb) }} GiB</small></div><div class="res-metric-sub">使用率 {{ numeric(summary.host_mem_used_percent) }}%</div><div class="res-metric-meter"><div class="res-metric-meter-fill" :style="meter(summary.host_mem_used_percent)"></div></div></article>
            <article class="res-metric-card"><div class="res-metric-label">存储水位</div><div class="res-metric-val">{{ numeric(summary.storage_used_tb, 2) }} <small>/ {{ numeric(summary.storage_total_tb, 2) }} TB</small></div><div class="res-metric-sub">使用率 {{ numeric(summary.storage_used_percent) }}%</div><div class="res-metric-meter"><div class="res-metric-meter-fill" :style="meter(summary.storage_used_percent)"></div></div></article>
          </section>

          <section class="res-section">
            <div class="res-section-title"><div class="res-section-title-left"><span>GPU 拓扑与实时遥测</span><span class="res-section-badge">{{ numeric(summary.total_gpus, 0) }} 张可观测设备</span></div></div>
            <p v-if="!gpus.length" class="empty">{{ dashboard.hardware_state === 'fresh' && dashboard.gpus ? '当前没有 GPU 设备。' : 'GPU 观测暂不可用。' }}</p>
            <div class="gpu-grid">
              <article v-for="g in gpus" :key="g.uuid || `${g.device}:${g.index}`" class="gpu-card">
                <div class="gpu-card-head"><span class="gpu-id">GPU {{ g.index }}</span><span class="gpu-name">{{ g.model_name || '型号未提供' }} · {{ scaled(g.total_vram_mb, 1024) }} GiB</span><span class="gpu-state" :class="valid(g.utilization) && g.utilization > 5 ? 'active' : 'standby'">{{ valid(g.utilization) ? g.utilization > 5 ? '运行中' : '低负载' : '利用率未知' }}</span></div>
                <div class="gpu-bar-wrap"><div class="gpu-bar-fill" :style="meter(ratio(g.used_vram_mb, g.total_vram_mb))"></div></div>
                <div class="gpu-stat-row"><span class="gpu-stat-vram">显存 <strong>{{ scaled(g.used_vram_mb, 1024) }}</strong> / {{ scaled(g.total_vram_mb, 1024) }} GiB</span><span class="gpu-stat-telemetry">{{ numeric(g.temperature_c, 0) }}°C · {{ numeric(g.power_watts, 0) }}W</span></div>
                <div class="gpu-workload"><span>利用率 {{ numeric(g.utilization) }}% · 剩余显存 {{ scaled(g.free_vram_mb, 1024) }} GiB</span></div>
              </article>
            </div>
          </section>

          <section class="res-section">
            <div class="res-section-title"><div class="res-section-title-left"><span>主机运算与系统内存</span><span class="res-section-badge">{{ numeric(node?.cpu_cores, 0) }} vCPUs · {{ scaled(node?.mem_total_bytes, 1024**3) }} GiB</span></div></div>
            <div class="res-host-grid">
              <article class="host-detail-card"><div class="host-detail-head"><div class="host-detail-title-wrap"><div class="host-detail-name">CPU 处理器运算单元</div></div><div class="host-detail-stat">{{ numeric(node?.cpu_utilization_percent) }}% <small>利用率</small></div></div><div class="res-metric-meter host-meter"><div class="res-metric-meter-fill" :style="meter(node?.cpu_utilization_percent)"></div></div>
                <div class="host-meta-grid"><div class="host-meta-item"><span class="host-meta-lbl">计算规格</span><strong class="host-meta-val">{{ numeric(node?.cpu_cores, 0) }} vCPUs</strong></div><div class="host-meta-item"><span class="host-meta-lbl">系统负载（1m / 5m / 15m）</span><strong class="host-meta-val">{{ numeric(node?.cpu_load1, 2) }} / {{ numeric(node?.cpu_load5, 2) }} / {{ numeric(node?.cpu_load15, 2) }}</strong></div></div>
              </article>
              <article class="host-detail-card"><div class="host-detail-head"><div class="host-detail-title-wrap"><div class="host-detail-name">系统运行内存池</div></div><div class="host-detail-stat">{{ scaled(node?.mem_used_bytes, 1024**3) }} <small>/ {{ scaled(node?.mem_total_bytes, 1024**3) }} GiB</small></div></div><div class="res-metric-meter host-meter"><div class="res-metric-meter-fill" :style="meter(node?.mem_used_percent)"></div></div>
                <div class="host-meta-grid"><div class="host-meta-item"><span class="host-meta-lbl">活跃占用</span><strong class="host-meta-val">{{ formatResource(node?.mem_used_bytes, 'bytes') }}</strong></div><div class="host-meta-item"><span class="host-meta-lbl">文件系统缓存</span><strong class="host-meta-val">{{ formatResource(node?.mem_cached_bytes, 'bytes') }}</strong></div><div class="host-meta-item"><span class="host-meta-lbl">剩余可用</span><strong class="host-meta-val">{{ formatResource(node?.mem_available_bytes, 'bytes') }}</strong></div><div class="host-meta-item"><span class="host-meta-lbl">缓冲区</span><strong class="host-meta-val">{{ formatResource(node?.mem_buffers_bytes, 'bytes') }}</strong></div></div>
              </article>
            </div>
          </section>

          <section class="res-section">
            <div class="res-section-title"><div class="res-section-title-left"><span>存储系统与持久化卷</span></div></div>
            <p v-if="!node?.storage_disks?.length" class="empty">{{ node?.storage_disks ? '当前没有可观测的文件系统。' : '存储观测暂不可用。' }}</p>
            <div class="res-storage-cards"><article v-for="disk in node?.storage_disks ?? []" :key="`${disk.device}:${disk.mountpoint}`" class="storage-card"><div class="storage-head"><div class="storage-mount-wrap"><span class="storage-mount">{{ disk.mountpoint }}</span><span class="storage-badge">{{ disk.fstype || '文件系统未知' }}</span></div><span class="storage-val">{{ formatResource(disk.used_bytes, 'bytes') }} <small>/ {{ formatResource(disk.total_bytes, 'bytes') }}</small></span></div><div class="storage-bar"><div class="storage-fill" :style="meter(disk.used_percent)"></div></div><div class="storage-meta"><span>{{ disk.device || '设备未提供' }}</span><span>可用 {{ formatResource(disk.avail_bytes, 'bytes') }} · 使用率 {{ numeric(disk.used_percent) }}%</span></div></article></div>
          </section>

          <section class="res-section">
            <div class="res-section-title"><div class="res-section-title-left"><span>已部署工作负载与容器用量</span><span class="res-section-badge">配置请求与真实采样分别呈现</span></div></div>
            <div class="workloads-container">
              <div class="quota-summary-strip"><div class="quota-item"><span class="quota-item-lbl">容器组</span><div class="quota-item-val">{{ dashboard.workloads_summary.total_pods }}</div></div><div class="quota-item"><span class="quota-item-lbl">活跃 Pod GPU 请求（非实际分配）</span><div class="quota-item-val">{{ dashboard.workloads_summary.total_gpu_requested }}</div></div><div class="quota-item"><span class="quota-item-lbl">活跃 Pod CPU 请求</span><div class="quota-item-val">{{ formatResource(dashboard.workloads_summary.total_cpu_requested) }}</div></div><div class="quota-item"><span class="quota-item-lbl">活跃 Pod 内存请求</span><div class="quota-item-val">{{ formatResource(dashboard.workloads_summary.total_memory_requested, 'bytes') }}</div></div></div>
              <div class="workload-filters"><div class="workload-tabs"><button class="workload-tab-btn" :class="{active:currentFilter === 'all'}" @click="currentFilter='all'">全部</button><button class="workload-tab-btn" :class="{active:currentFilter === 'gpu'}" @click="currentFilter='gpu'">配置 GPU</button><button class="workload-tab-btn" :class="{active:currentFilter === 'infra'}" @click="currentFilter='infra'">未配置 GPU</button></div><div class="workload-search"><input v-model="searchQuery" type="search" aria-label="搜索工作负载" placeholder="搜索应用或命名空间"></div></div>
              <p v-if="!workloads.length" class="empty">暂无工作负载。</p><p v-else-if="!filteredWorkloads.length" class="empty">没有符合筛选条件的工作负载。</p>
              <div class="workload-list"><article v-for="wl in filteredWorkloads" :key="wl.pod_uid" class="wl-card" :class="{'has-gpu':wl.type === 'gpu'}"><div class="wl-head"><div class="wl-title-box"><div class="wl-title-row"><span class="wl-name">{{ wl.display_name || wl.name }}</span><span class="wl-namespace">{{ wl.namespace }}</span></div><span class="wl-pod-id">{{ wl.name }}</span></div><div class="wl-meta-right"><span class="wl-uptime">{{ wl.age }}</span><span class="wl-status-tag">{{ wl.status }}</span></div></div><div v-for="container in wl.containers" :key="container.name"><div class="wl-foot"><span>{{ container.name }} · {{ container.ready ? '就绪' : '未就绪' }} · 重启 {{ container.restarts }} 次</span></div><div class="wl-metrics-grid"><div v-for="kind in (['cpu','memory'] as const)" :key="kind" class="wl-metric-cell"><div class="wl-cell-title">{{ kind === 'cpu' ? 'CPU 使用' : '内存工作集' }}</div><div class="wl-actual-val">{{ resourceValue(container[kind], kind === 'cpu' ? 'cores' : 'bytes') }}</div><div class="wl-metric-footnote">请求 {{ formatResource(container[kind].request, kind === 'cpu' ? 'cores' : 'bytes') }} · 上限 {{ formatResource(container[kind].limit, kind === 'cpu' ? 'cores' : 'bytes') }}</div><div class="wl-metric-footnote">{{ resourceState(container[kind]) === 'fresh' ? '采样 '+new Date(container[kind].sampled_at!).toLocaleString('zh-CN') : resourceState(container[kind]) === 'stale' ? '采样已过期' : '未采集' }}</div></div><div class="wl-metric-cell"><div class="wl-cell-title">GPU 配置请求</div><div class="wl-actual-val">{{ container.gpu_request ?? '—' }}</div><div class="wl-metric-footnote">实际设备分配与显存用量待绑定确认</div></div></div></div></article></div>
            </div>
          </section>
        </div>
      </template>
    </div>
  </main>
</template>

<style scoped>
.main{margin:0;padding:28px 36px 48px;min-width:0}.workspace{width:100%}.heading{display:flex;align-items:flex-end;justify-content:space-between;gap:20px;margin-bottom:20px}.heading h1{font-size:30px;font-weight:600;letter-spacing:-.6px}.heading p{color:var(--muted);margin-top:6px;font-size:13px}.refresh{padding:8px 16px;border:1px solid var(--line);border-radius:9px;background:var(--shell-panel)}.notice{padding:16px;border:1px solid var(--line);border-radius:9px;margin-bottom:16px}.error{color:var(--danger)}.observation{color:var(--muted);font-size:12px;margin-bottom:16px}.empty{padding:14px;color:var(--muted)}.resource-dashboard{overflow:visible}.storage-meta{flex-wrap:wrap}.wl-pod-id{overflow-wrap:anywhere}@media(max-width:700px){.main{padding:20px 16px}.heading{align-items:flex-start}.heading h1{font-size:26px}.wl-metrics-grid{grid-template-columns:1fr}}
</style>
