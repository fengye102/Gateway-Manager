# Gateway-Manager 项目规范 (GEMINI.md)

## 1. 项目定位与核心约束
- **项目本质**：本机/内网大模型 API 网关聚合、多 Key 健康选路与策略/规则注入代理（`gateway-manager` v2.6.0）。
- **零外部依赖原则**：
  - 后端仅使用 **Go 标准库**（`CGO_ENABLED=0`），严禁引入任何第三方 Go Module。
  - 前端为纯原生技术栈（Vanilla HTML5 + Modern CSS + ES6+ JS），通过 Go 标准库 `embed.FS` 编译进单一可执行文件；**严禁引用外部 CDN（字体、图标库、JS 框架）**，确保纯内网与离线环境 100% 可用。

## 2. 目录结构、命名与清理规范
工作区根目录 `d:\code\Program\Gateway-Manager-2.6.0` 即 Go Module 根目录（消除冗余嵌套层）：

| 路径 | 职责说明 | 命名约定 | 清理策略 |
| :--- | :--- | :--- | :--- |
| `GEMINI.md` | 工作区工程、架构与前端设计规范 | 固定大写 `GEMINI.md` | 随架构/规范变更先行更新，长期保留 |
| `*.go` | 网关核心逻辑、管理 API 与测试 | 小写 `snake_case.go`，测试 `*_test.go` | 废弃源文件直接删除，禁止留 `.bak` |
| `webui/` | 控制台前端静态资源（通过 `//go:embed` 嵌入） | 小写 `kebab-case`（`index.html`、`styles.css`、`app.js`） | 仅保留运行时必需静态资源，禁止存放构建垃圾 |
| `deploy/` | Prometheus、Grafana 与系统服务脚本 | 小写 `kebab-case` | 随部署方案同步维护 |
| `docker/` | 容器启动脚本与监控探针配置 | 小写 `kebab-case` | 随镜像构建规范同步维护 |
| `build/`, `dist/` | 本地与交叉编译二进制产物 | `gateway-manager-<os>-<arch>[.exe]` | 属于临时构建目录，纳入 `.gitignore`，发布或验证后按需清理，严禁入库 |
| `snapshots/`, `*.state.json`, `smoke*` | 运行时快照、状态持久化与冒烟测试临时文件 | 运行时自动生成 | 纳入 `.gitignore` / `.dockerignore`，测试产生的临时文件测完即删 |

## 3. 前端架构与视觉交互规范 (`webui/`，对齐 `cli2api` Studio 设计语言)
1. **代码解耦**：禁止在 `.go` 文件中硬编码超长 HTML/CSS/JS 字符串。`webui.go` 仅负责 `//go:embed webui/*` 与资源读取路由，前端结构、样式、交互逻辑分别置于 `webui/index.html`、`webui/styles.css`、`webui/app.js`（并在 `Dockerfile` 中同步 `COPY webui ./webui`）。
2. **布局骨架（参考 `cli2api` 控制面）**：
   - **可折叠左侧栏（`260px` 展开 / `76px` 图标轨，移动端抽屉）**：采用 `--surface-secondary` 底色无硬框分割；顶部为几何开放圆环 BrandMark（随主题墨色描边 + `#22D3EE` 青色端点与加载扫描光效）；底部常驻 `.sidebar-status` 运行状态卡与实时并发指示器。
   - **顶部极简栏（Topbar）**：左侧展示呼吸状态圆点 + 页面标题 + 单行描述；右侧采用 `.toolbar--attached` 连体细线工具条（自动刷新暂停/恢复、立即刷新、Admin Token 锁、深浅主题切换）。
   - **主内容区**：最大宽度 `1480px` 居中自适应工作区。
3. **信息分层**：
   - **概览看板**：顶部单行 `LiveStrip`（实时并发负载条 + 代理端点复制 + 近 60s 请求脉搏折线）+ 4 张主指标卡 + 3 张次级指标卡 + 请求治理分布与重试参数 + A/B 实验与联邦集群表。
   - **上游治理**：`.data-toolbar` 搜索与分段筛选条；主表采用扁平 `--surface-secondary` 表头与细分割线；Key 池健康分采用 `12` 段式 `.runtime-meter` 均衡器指示条与可展开抽屉呈现。
   - **规则引擎**：使用凸起分段滑块（Segmented Tabs）将「规则流编排画布」「规则试算器」「回放与回归」「规则书 DSL」分层组织。
4. **Studio Grayscale 视觉与主题系统**：
   - 遵循 **零色度中性灰阶（Zero-Chroma `oklch(... 0 0)`）** 体系：主操作按钮为高对比度墨色（深色下近白墨色，浅色下近黑墨色），语义色仅用于状态圆点、徽标与图表序列。
   - 卡片采用 **Solid-Glass** 几何质感：`14px` 圆角、零实线外框（`border: 0`），通过顶部微光内描边 `--surface-rim` 与微投影区分层级；卡片内部嵌套块一律采用扁平 `--surface-secondary` 色块（禁止框套框）。
   - 内置 **深色（默认）/ 浅色** 双主题切换并持久化至 `localStorage`。
   - 统一系统字体栈，所有监控数值、延迟、ID 强制启用 `font-variant-numeric: tabular-nums`。
5. **交互反馈**：
   - **全面禁用原生 `alert()` / `prompt()` / `confirm()`**，统一使用模糊遮罩居中 Modal 对话框与右下角 `Toaster` 浮动通知。
   - 轮询刷新支持「暂停/恢复」并在标签页隐藏（`document.hidden`）时静默跳过，刷新时保留用户展开状态、过滤条件与规则未保存态。
6. **接口契约**：严格兼容 `webui_handlers.go` 现有的 `/admin/api/*` 路径、请求体结构与 `X-Admin-Token` 鉴权头。

## 4. 变更与质量门禁
- **规范优先**：调整目录结构、接口或前端设计规范前，先更新本 `GEMINI.md`，再修改代码。
- **命名与语言**：代码、文件名、CSS 类名、JS 变量与命令统一使用英文；界面文案、注释与用户沟通使用中文。
- **提交前必跑检查**（与 `.github/workflows/ci.yml` 对齐）：
  1. `gofmt -l .` 输出为空
  2. `go vet ./...` 零告警
  3. `go test -count=1 ./...` 全部通过
  4. `go build -trimpath .` 编译成功
