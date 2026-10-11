// Consumer types for the central app-dashboard-v1 contract. No host implementation.
export type Lifecycle = 'active' | 'hidden' | 'dispose'
export interface AppSDK {
  version: string
  app: { request(input: { path: string; method: 'GET' }, options?: { signal?: AbortSignal }): Promise<{ status: number; data: unknown; requestId: string }> }
  lifecycle: { subscribe(listener: (event: Lifecycle) => void): () => void }
  context: { subscribe(listener: () => void): () => void }
  theme: { get(): 'light' | 'dark'; subscribe(listener: (theme: 'light' | 'dark') => void): () => void }
}
export interface DashboardHost {
  ready(): void
  resize(height: number): void
}
declare global { interface Window { $wujie?: { props?: { appSDK?: AppSDK; dashboardHost?: DashboardHost } } } }

export function hostProps() {
  const props = window.$wujie?.props
  if (!props?.appSDK || !props.dashboardHost || !/^1\.(?:[1-9]\d*)\./.test(props.appSDK.version) || !props.appSDK.theme) return null
  return { sdk: props.appSDK, host: props.dashboardHost }
}
