# verdantflare-app-resources

VerdantFlare AppCenter 资源管理应用。

## 1. 工程范围

Go 服务独立提供资源总览、GPU／主机／存储观测和工作负载分类汇总；Vue Dashboard 由 Studio 在运行时加载。底层采集、身份授权与调度继续使用 Station。应用不需要数据库，不直连 Prometheus 或 Kubernetes。

当前版本为 `0.1.0`，最低 AppSDK 为 `1.1.0`。前端通过宿主注入的 `appSDK.app.request`、`theme`、`context`、`lifecycle` 消费能力，通过 `dashboardHost` 完成就绪与尺寸握手；不导入 Studio 私有 Store 或 Wails 绑定。

## 2. 运行配置

| 配置项 | 用途 |
| --- | --- |
| `APP_SERVICE_TOKEN` | Studio 到应用的专属服务凭据 |
| `STATION_RESOURCES_TOKEN` | 应用到 Station 资源事实接口的专属服务凭据 |
| `STATION_URL` | 内部 Station 根地址 |
| `STATION_ID` | 应用允许访问的 Station 标识 |
| `RESOURCES_LISTEN` | 默认 `0.0.0.0:8000` |
| `RESOURCES_ASSETS_DIR` | 默认 `frontend/dist` |

凭据由受控配置或 Secret 注入。业务接口校验服务凭据及 Station 范围，再由 Station 对当前用户、组织、管理员角色与 `app:read` 范围进行实时复核。普通身份头、预装身份及静态页面可访问性都不构成业务授权。

内部健康入口为 `/healthz`，Dashboard 入口为 `/dashboard/index.html`，业务读取为 `/api/v1/dashboard`；应用从 Station 的 `/internal/v1/resources/snapshot` 消费事实。`/internal/dashboard/declaration` 与 `/internal/dashboard/index` 返回发行声明和原字节索引。启动时检查制品，文件类型或摘要不符即拒绝启动；仅允许索引内资源。机器契约与部署事实仅在中央设计仓库维护。

## 3. 验证与交付

不依赖服务的单元验证使用 `go test ./...`；前端安装固定依赖后使用 `pnpm check` 与 `pnpm test`。这些检查不启动服务或容器。

GitHub Actions 在 `release` 分支按 `VERSION` 编译前后端、生成 Dashboard／导航声明与资源索引、构建并推送 `resources-v<版本>`，发布前拒绝覆盖已有标签。运行、Web／Wails 接入及独立升级验收在获授权的 5090 dev 环境进行，不能以本机单元检查替代。

## 4. 当前依据

- [预装应用迁移计划](https://github.com/verdantflarehub/verdantflare-design/blob/dev/plan/archive/2026.10.studio.appcenter.preinstalled.plan.md)。
- [预装应用迁移设计](https://github.com/verdantflarehub/verdantflare-design/blob/dev/changes/archive/change_20261011_preinstalled_apps.md)。
- [AppDashboard 契约](https://github.com/verdantflarehub/verdantflare-design/blob/dev/docs/design/contracts/app-dashboard-v1.md)。
- [GitHub Actions 指南](https://github.com/verdantflarehub/verdantflare-design/blob/dev/docs/GithubAction.md)。
