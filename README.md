# 网关管家 · 桌面版（gateway-manager v2.2.0）

**中文** | [English](README.en.md)

[![ci](https://github.com/fengye102/Gateway-Manager/actions/workflows/ci.yml/badge.svg)](https://github.com/fengye102/Gateway-Manager/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/fengye102/Gateway-Manager?color=blue)](https://github.com/fengye102/Gateway-Manager/releases/latest)
[![license](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
[![go](https://img.shields.io/badge/go-1.21%2B-00ADD8)](go.mod)


把逆向 **网关管家 3.4.10（dev.pojia2api）** 得到的协议语义，重实现成一份 **Windows / Linux / macOS 通用** 的本机网关：

- 单文件二进制，纯 Go 标准库，**零第三方依赖**
- OpenAI / Anthropic 兼容接口，按「策略 / 规则」注入 system 指令后转发
- 多上游选路 + **多 key 健康打分池** + 失败切换 + 冷却 + RPM / 并发限制
- **拖拽式规则流编辑器** + **策略模板库** + **规则命中热力图**，全部在 Web 控制台里点几下就能改
- **状态持久化**：key 健康分 / 冷却 / 规则命中统计落盘，重启不丢（只存指纹，不存明文 key）
- **Prometheus `/metrics`**：请求、上游、key、规则、快照全维度指标 + **延迟直方图**（p50/p95/p99 直接算），附 Grafana 看板 JSON
- **拦截回放**：被规则拦下的请求留档（Header 脱敏），控制台一键复现看新规则会不会放行
- **A/B 灰度**：同优先级上游按 @@weight@@ 比例分流，带 @@gateway_manager_routed_total@@ 分流指标
- **告警规则**：@@deploy/prometheus-alerts.yml@@ 9 条（上游挂掉 / key 全冷却 / p95 超阈 / 拦截激增 / 鉴权爆破…）
- **耗时分解**：排队（选路+许可）· 上游首字节 TTFB · 响应体转发，三个直方图分别可算分位
- **自定义健康探测**：每个上游可配 @@probe@@（路径/方法/期望状态码/期望内容）
- **成功请求采样**：按 N 抽样留档（脱敏），用于回归验证
- **规则书 DSL**：纯文本写规则，与拖拽 UI 双向同步，错误带行号
- **多实例联邦**：一个控制台并行汇总多个 gateway-manager 实例（版本/请求/上游健康）
- **上游额度预算**：日请求 / 日 token 预算，用尽自动跳过该上游并暴露指标
- **批量回归**：拿历史采样/回放批量跑当前规则链，输出拦截率与规则命中分布
- **跨实例转发**：联邦 peer 可声明为上游，按权重/优先级把流量打到别的实例
- **token 精确计量**：解析响应 @@usage@@（含 SSE 尾块），预算从估算自动纠正为实际值
- **上游适配器**：用配置声明厂商差异（路径 / 认证头 / 额外头），不写代码就能接非标准网关
- **条件表达式**：@@when.expr@@ 支持 @@~@@ 正则、@@and/or@@、@@!@@ 取反、@@header:X@@ 取值
- **分流镜像**：按比例把请求影子复制到另一个上游做对比，指标齐全
- **快照 Git 化**：每次快照自动 commit 到本地 git，可看历史、可手动提交
- **熔断半开**：冷却到期先放少量探测，成功即闭合，失败按指数退避续开
- **A/B 实验**：按权重分臂，每臂可绑策略与上游，自动统计次数/错误/延迟/token
- **一键安装**：@@install.sh@@ / @@install.ps1@@ 装好二进制、配置、systemd/Windows 服务
- **配置快照与回滚**：改配置/规则/模板前自动留一版，控制台一键查看与回滚
- 自动发现本机其它 OpenAI 兼容网关

> 与安卓版的区别：不含任何厂商账号登录与私有接口适配（那属于抓 Cookie 复用网页通道，各家 ToS 明确禁止），只做「本机网关聚合 + 注入 + 代理治理」这一层。

---

## 1. 快速开始

### Windows（推荐：下载 zip 解压）

1. 到 [Releases](https://github.com/fengye102/Gateway-Manager/releases/latest) 下载 `gateway-manager-<版本>-windows-amd64.zip`
2. **右键 zip → 属性 → 勾选「解除锁定」**（从网上下载的文件会被 SmartScreen 标记，不解除会被拦）
3. 解压后双击 `gateway-manager-windows-amd64.exe` 或 `start-windows.bat`
   - 首次运行会在同目录**自动生成 `config.json`**，然后直接开始监听 `127.0.0.1:7878`
   - 会自动打开控制台 `http://127.0.0.1:7878/ui`；关闭窗口（或 Ctrl+C）即停止
   - 出错时窗口不会一闪而过，会停在「按回车键退出...」

```powershell
# 命令行方式（等价）
.\gateway-manager-windows-amd64.exe init -config config.json
.\gateway-manager-windows-amd64.exe discover -priority     # 扫本机已有网关
notepad config.json                                        # 填 upstreams[].base_url / api_keys
.\gateway-manager-windows-amd64.exe serve -config config.json
```

> 如果 Windows Defender / 国产杀软报毒：未签名 Go 二进制常被误报，可添加排除项，或改用 `go build` 自行编译；发行包附带 `SHA256SUMS.txt` 可校验完整性。

### Linux / macOS

```bash
curl -LO https://github.com/fengye102/Gateway-Manager/releases/latest/download/gateway-manager-2.2.0-linux-amd64.tar.gz
tar xzf gateway-manager-2.2.0-linux-amd64.tar.gz
./gateway-manager-2.2.0-linux-amd64 init -config config.json
./gateway-manager-2.2.0-linux-amd64 serve -config config.json
```

### Docker

```bash
docker compose up -d     # gateway-manager + Prometheus + Grafana
```

启动后：

```
 监听      : 127.0.0.1:7878
 OpenAI    : http://127.0.0.1:7878/v1/chat/completions
 控制台    : http://127.0.0.1:7878/ui
 指标      : http://127.0.0.1:7878/metrics
```

客户端 base_url 指向 `http://127.0.0.1:7878/v1` 即可；配了 `access_keys` 就带 `Authorization: Bearer <key>` 或 `x-api-key`。

### 常见启动问题

| 现象 | 处理 |
| --- | --- |
| 双击后窗口一闪就没 | 已修：v2.2.0 起会自动生成配置并常驻；旧版请用 `start-windows.bat` 或命令行启动 |
| 提示「Windows 已保护你的电脑」 | 右键 exe → 属性 → 解除锁定；或「更多信息 → 仍要运行」 |
| 端口 7878 被占用 | `serve -config config.json -listen 127.0.0.1:8787` |
| 杀软拦截 | 未签名二进制误报，可加排除项或自行编译 |
| 想局域网访问 | 把 `server.bind` 改 `0.0.0.0`，并务必设置 `access_keys` 与 `admin_token` |

## 2. 子命令

| 命令 | 说明 |
| --- | --- |
| `serve` | 启动网关（默认子命令） |
| `discover` | 扫本机 OpenAI 兼容网关（`-range 3000-9999` / `-priority` / `-workers` / `-json`） |
| `check` | 一次性健康检查，打印耗时与模型 |
| `init` | 生成示例配置（`-force` 覆盖） |
| `dump` | 打印归一化后的配置 |
| `version` | 版本 |

## 3. 请求流水线

```
POST /v1/chat/completions
  │
  ├─ 1. 鉴权（access_keys：Authorization / x-api-key）
  ├─ 2. 解析 JSON，取 model / stream
  ├─ 3. 规则流（rules，顺序执行，见 §6）
  │      · deny  → 直接返回错误码
  │      · override → 改写 model / temperature 等参数
  │      · header → 给上游加请求头
  │      · inject → 收集注入文本（命中则不再叠加策略注入）
  ├─ 4. 候选上游（enabled + 模型可解析）→ 优先级排序 → 同优先级轮转
  ├─ 5. 取上游许可（冷却 / 并发 / RPM）+ **从 key 池挑评分最高的 key**
  ├─ 6. 注入（规则文本 或 profile 策略）→ 转发
  ├─ 7. 失败判定：传输错误 / {401,403,408,429} / 5xx
  │      · key 类失败且还有别的 key → **同一个上游换 key 重试**
  │      · 其它失败 → 换下一个上游
  └─ 8. 成功：SSE 透传 或 （stream=false 时）合流成单个 JSON
```

## 4. 配置说明

完整示例见 `config.example.json`。

### 4.1 server

| 字段 | 默认 | 说明 |
| --- | --- | --- |
| `bind` / `port` | 127.0.0.1 / 7878 | 监听地址与端口 |
| `access_keys` | [] | 代理 API key，空 = 不鉴权 |
| `admin_token` | "" | 控制台口令（空 = 不校验） |
| `enable_admin_ui` | true | 是否开放 `/ui` |
| `max_body_mb` | 32 | 请求体上限 |
| `health_interval_seconds` | 30 | 上游健康检查周期 |
| `log_limit` | 500 | 日志保留条数 |
| `merge_sse` | true | 非流式请求遇到 SSE 时合流 |
| `key_strategy` | score | key 选择策略：`score`（健康打分）或 `round_robin` |
| `key_cooldown_seconds` | 60 | key 冷却基数（401/403 会 ×5，429 ×1） |

### 4.2 retry

| 字段 | 默认 | 说明 |
| --- | --- | --- |
| `max_retries` | 2 | 额外重试次数（总尝试 = 它 + 1，另加每个上游的换 key 额度，上限 8） |
| `failure_threshold` | 2 | 上游连续失败多少次进冷却 |
| `cooldown_seconds` | 60 | 上游冷却时长 |
| `request_timeout_seconds` | 180 | 单请求超时 |
| `retry_status` | [401,403,408,429] | 触发换上游/换 key 的状态码（≥500 恒触发） |

### 4.3 upstreams

| 字段 | 说明 |
| --- | --- |
| `id` / `name` | 标识（也用作 `id/model` 前缀路由）与展示名 |
| `base_url` | 上游根地址 |
| `api_keys` | **key 池**，按健康分选路（可多个，做多账号轮换） |
| `models` | 提供哪些模型；空 = 接受任何模型 |
| `mappings` | 对外别名 → 真实模型名 |
| `profile_id` | 固定使用的策略 |
| `priority` / `rpm` / `concurrency` / `enabled` | 选路与限流 |
| `headers` | 追加请求头 |

### 4.4 federation

| 字段 | 说明 |
| --- | --- |
| @@enabled@@ | 是否在控制台展示联邦总览 |
| @@timeout_ms@@ | 拉取单个 peer 的超时（默认 3000） |
| @@peers[]@@ | @@{id, name, base_url, token, as_upstream, access_key, models, priority, weight}@@ |
| 说明 | @@token@@ 用于读对端状态；@@as_upstream=true@@ 时以 @@peer-<id>@@ 的身份成为本实例的上游，@@access_key@@ 是对端 @@access_keys@@ 之一 |

### 4.5 profiles / rules / templates

- `profiles`：`id / name / models（子串匹配，空=兜底）/ instruction / enabled`
- `rules`：见 §6，字段随节点类型不同
- `templates`：自定义策略模板（`id / name / category / instruction / tags`），内置 12 个不可覆盖

## 5. 多 key 池与健康打分（新）

- 每个上游的 `api_keys` 会变成独立 key 池，每个 key 维护：评分（0–100）、成功/失败数、连续失败、冷却截止、最近状态码、延迟 EMA
- 初始 80 分，成功 +6（上限 100），每分钟空闲自然回升 +2
- 失败扣分并可能冷却：
  - `401 / 403`（凭证失效）−60 分，冷却 = `key_cooldown_seconds × 5`
  - `429`（被限流）−25 分，冷却 = 基础值
  - `5xx` / 传输错误 −15 / −20 分
  - 连续失败 ≥ 2 次才进冷却，避免抖动
- 选路：`key_strategy=score` 时取评分最高（同分取最久未用）；`round_robin` 时按序轮转
- 全部 key 冷却 → 该上游本次跳过（计入 `key_skips` 统计），继续试下一个上游
- 控制台「上游」页直接看到每个 key 的遮罩、评分条、状态、成功/失败

## 6. 规则流（拖拽式，新）

控制台「规则」页 = 一条从上到下的节点链，**拖动卡片即改顺序**，每个节点可加条件：

| 节点 | 作用 | 主要字段 |
| --- | --- | --- |
| 条件 match | 只做条件标记，不改请求 | `condition` |
| 注入 inject | 往请求里塞 system 文本 | `instruction` 或 `profile_id` |
| 改写 override | 改 `model` / 覆盖参数 | `set_model`、`set_params` |
| 请求头 header | 给上游加 header | `header_name`、`header_value` |
| 拦截 deny | 命中即返回错误 | `status`、`message` |

条件字段（留空表示总是命中）：

```json
{
  "models": ["mini", "coder"],        // model 子串包含任一
  "paths": ["/v1/messages"],          // 路径前缀
  "header_key": "X-Client",           // 请求头存在
  "header_value": "android",          // 且值包含
  "body_contains": "\"messages\":[]"  // 原始请求体包含文本
}
```

语义要点：

1. 节点按顺序执行，`disabled: true` 的跳过
2. 任一 `deny` 命中立刻返回（已收集的注入文本仍会出现在试算结果里，便于排查）
3. 有 `inject` 节点命中时，**不再叠加 profile 策略注入**（不会双重注入）；没命中则回落到策略
4. `override` 的 `set_model` 会改变后续的模型匹配与上游选择
5. 页面里的「规则试算」是 dry-run：输入 model / path / body，立刻看到命中、跳过、拦截、注入文本与最终请求体

## 7. 策略模板库（新）

- 内置 12 个模板：严谨事实核查、资深工程师、代码审查、测试用例生成、SQL 助手、结构化提取、翻译润色、技术文档、长文摘要、极简回答、安全评估、循序渐进讲解
- 支持搜索（名称 / 分类 / 标签）、预览、**一键应用到策略**（新建或覆盖）、另存为自定义模板、删除自定义模板
- 内置模板不可覆盖、不可删除；自定义模板存进配置的 `templates` 字段

接口：`GET /admin/api/templates`、`POST /admin/api/templates`（新增/更新）、`POST /admin/api/templates/delete`、`POST /admin/api/templates/apply`（`{template_id, mode:new|overwrite, profile_id, name}`）。

## 8. 状态持久化（新）

运行状态落在一个独立的状态文件里（默认与配置同目录，例如 @@config.state.json@@），可用 @@-state@@ 指定：

@@@bash
gateway-manager serve -config config.json -state /var/lib/gateway-manager/state.json
@@@

- 内容：每个上游的 **key 指纹 + 评分 + 成功/失败 + 连续失败 + 冷却截止 + 最近状态码 + 延迟 EMA + 请求数**，以及**规则命中/跳过/拦截计数**
- 只存 @@sha256(key) 前 8 字节（16 位十六进制指纹）@@，**状态文件里没有明文 key**；config 变更换 key 后指纹对不上就自动忽略
- 写入时机：每 @@persist_interval_seconds@@ 秒检查一次「有变化才写」，原子写（tmp + rename），退出时再存一次
- 启动时先 @@LoadState@@ 再建 key 池，评分与冷却原样恢复；@@persist_state=false@@ 则完全关闭
- 接口暴露：@@/admin/api/status@@ 里的 @@persist@@ 字段（enabled / path / last_save / dirty）

## 9. 规则热力统计（新）

每次请求跑完规则链都会记账：

| 计数 | 含义 |
| --- | --- |
| matched | 条件命中并执行了该节点 |
| skipped | 条件不匹配被跳过 |
| denied | 该节点触发了拦截 |

- 规则页每个节点卡片下方有热力条：占比 = @@matched / (matched + skipped)@@，绿 → 黄 → 红 表示越常用
- 页面顶部「规则热力」表按节点列出命中 / 跳过 / 拦截 / 占比 / 最近命中时间
- @@GET /admin/api/rules/stats@@ 读统计，@@DELETE /admin/api/rules/stats@@ 清零（清零也会落盘）
- dry-run（规则试算）**不计入**统计，方便调试

## 10. Prometheus 指标（新）

@@@bash
curl -s http://127.0.0.1:7878/metrics | head
@@@

输出为标准 text exposition 格式（@@text/plain; version=0.0.4@@），指标族：

| 指标 | 类型 | 说明 |
| --- | --- | --- |
| @@gateway_manager_info{version,bind,port,key_strategy}@@ | gauge | 构建与监听信息（恒 1） |
| @@gateway_manager_uptime_seconds@@ | gauge | 运行时长 |
| @@gateway_manager_requests_total@@ | counter | 代理请求总数 |
| @@gateway_manager_profile_injections_total@@ / @@gateway_manager_rule_injections_total@@ | counter | 策略 / 规则注入次数 |
| @@gateway_manager_no_profile_total@@ / @@gateway_manager_inject_conflict_total@@ | counter | 未注入 / 注入冲突 |
| @@gateway_manager_rule_denies_total@@ / @@gateway_manager_auth_failures_total@@ / @@gateway_manager_upstream_errors_total@@ / @@gateway_manager_key_skips_total@@ | counter | 拦截 / 鉴权失败 / 上游失败 / key 全冷却跳过 |
| @@gateway_manager_upstream_{enabled,healthy,cooldown_seconds,requests_total,successes_total,failures_total,inflight,latency_ms,models}{upstream,name}@@ | gauge/counter | 每上游状态 |
| @@gateway_manager_key_{score,successes_total,failures_total,cooling,requests_total}{upstream,key}@@ | gauge/counter | 每把 key（label 用遮罩值） |
| @@gateway_manager_rule_{matched_total,skipped_total,denied_total,hit_ratio}{rule,name}@@ | counter/gauge | 每规则热度 |
| @@gateway_manager_config_{upstreams,profiles,rules}@@ | gauge | 配置规模 |
| @@gateway_manager_state_last_save_timestamp@@ / @@gateway_manager_snapshots@@ / @@gateway_manager_replay_entries@@ | gauge | 状态落盘时间 / 快照数量 / 回放条数 |
| @@gateway_manager_request_duration_seconds_bucket{le}@@ / @@_sum@@ / @@_count@@ | histogram | 网关侧总耗时（0.05s…180s 共 12 桶 + @@+Inf@@） |
| @@gateway_manager_upstream_duration_seconds_{bucket,sum,count}{upstream,name}@@ | histogram | 上游调用耗时 |
| @@gateway_manager_upstream_responses_total{upstream,name,code}@@ | counter | 上游响应码分布 |
| @@gateway_manager_requests_by_endpoint_total{endpoint}@@ | counter | 按端点（chat.completions / responses / messages）的请求数 |
| @@gateway_manager_upstream_weight{upstream,name}@@ | gauge | 灰度权重 |
| @@gateway_manager_routed_total{model,upstream,name}@@ | counter | 按模型统计的实际分流次数（验证灰度比例） |
| @@gateway_manager_queue_duration_seconds_{bucket,sum,count}@@ | histogram | 排队耗时（选路 + 许可 + key 选择） |
| @@gateway_manager_upstream_ttfb_seconds_{bucket,sum,count}@@ | histogram | 上游首字节耗时 |
| @@gateway_manager_relay_duration_seconds_{bucket,sum,count}@@ | histogram | 响应体转发耗时 |
| @@gateway_manager_budget_{requests,tokens}_{used,limit}{upstream,name}@@ | gauge | 日预算用量与上限 |
| @@gateway_manager_budget_exhausted{upstream,name}@@ | gauge | 预算是否用尽（1/0） |
| @@gateway_manager_tokens_total{upstream,name}@@ | counter | 按上游累计的真实 token 用量（来自响应 usage） |
| @@gateway_manager_mirror_total{upstream,name,result}@@ | counter | 影子投递结果（ok / fail） |
| @@gateway_manager_upstream_circuit{upstream,name,state}@@ | gauge | 熔断状态（closed/half_open/open 各一行，当前态为 1） |
| @@gateway_manager_experiment_total{experiment,arm,result}@@ | counter | 实验各臂请求数（result=all/error） |
| @@gateway_manager_experiment_{latency_ms,tokens}_sum{experiment,arm}@@ | counter | 实验各臂累计耗时 / token |

### Grafana 看板

@@@bash
# 1) 让 Prometheus 抓 gateway-manager
cp deploy/prometheus.yml /etc/prometheus/conf.d/gateway-manager.yml   # 或合并进现有 scrape_configs
# 2) Grafana → Dashboards → Import → 上传 deploy/grafana-dashboard.json，数据源选 Prometheus
@@@

看板 @@uid: gateway-manager-main@@，12 个面板：QPS、上游健康、注入构成、拦截/错误、配置规模、运行时长、上游延迟 p95/p50、按端点速率、注入与拦截速率、上游响应码、key 健康评分表、规则命中占比条形图。

## 11. A/B 灰度分流（新）

- 给同 @@priority@@ 的上游填 @@weight@@：@@canary:1 / stable:9@@ ≈ 10% / 90%
- 选路：加权均匀铺开（用请求序号对权重和取模，不是随机，比例稳定可复现）；权重全为 0 时退回均分轮转
- 观测：@@gateway_manager_routed_total{model,upstream,name}@@ 累加实际分流次数，@@gateway_manager_upstream_weight@@ 暴露当前权重；控制台「上游」表新增权重列
- 灰度做法：先给 canary 权重 1、stable 权重 9，观察 @@gateway_manager_upstream_responses_total{code!="200"}@@ 与 @@gateway_manager_upstream_duration_seconds@@ 分位；没问题就调成 5:5，最后 9:1

## 12. 告警规则（新）

@@@bash
cp deploy/prometheus-alerts.yml /etc/prometheus/rules/     # rule_files 指向该目录即可
promtool check rules /etc/prometheus/rules/prometheus-alerts.yml
@@@

9 条规则（分 4 组）：

| 告警 | 触发条件（简写） | 级别 |
| --- | --- | --- |
| GatewayManagerUpstreamDown | @@gateway_manager_upstream_healthy == 0@@ 持续 2m | warning |
| GatewayManagerAllKeysCooling | 该上游所有 key 的 @@key_cooling@@ 全为 1 | critical |
| GatewayManagerKeyScoreLow | @@gateway_manager_key_score < 30@@ 持续 5m | warning |
| GatewayManagerUpstreamErrorRatio | 5m 错误率 / 请求率 > 20% | critical |
| GatewayManagerP95LatencyHigh | @@histogram_quantile(0.95,…) > 15s@@ 持续 10m | warning |
| GatewayManagerAuthFailureBurst | 10m 鉴权失败 > 20 次 | warning |
| GatewayManagerRuleDenySpike | 拦截速率 > 5/s 持续 5m | warning |
| GatewayManagerInjectConflict | 10m 注入冲突 > 5 次 | info |
| GatewayManagerStateSaveStale | 状态文件 15m 未更新 | warning |

## 13. 耗时分解（新）

一次请求被拆成三段，三段各有直方图，可以分别求分位定位瓶颈：

@@@
queue  选路 + 上游许可（冷却/并发/RPM）+ key 选择
ttfb   上游首字节（含建连、TLS、上游排队）
relay  响应体回写（流式场景 = 整个流的时长）
@@@

- 三段相加 ≈ @@gateway_manager_request_duration_seconds@@，差距大会暴露测量误差或客户端慢
- 典型用法：@@ttfb@@ 高 → 上游/网络慢；@@queue@@ 高 → 并发或 RPM 打满；@@relay@@ 高 → 流式输出长或客户端读得慢

## 14. 自定义健康探测（新）

@@@json
{
  "id": "remote",
  "base_url": "https://api.example.com",
  "probe": {
    "path": "/v1/models",
    "method": "GET",
    "expect_status": [200],
    "expect_contains": "gpt-4o-mini",
    "timeout_ms": 3000
  }
}
@@@

- 不配 @@probe@@ 时按老规矩 @@GET /v1/models@@（并解析模型列表）
- 配了 @@probe@@ 就完全按它来：方法支持 GET/HEAD/POST/PUT/PATCH/DELETE（写方法带空 JSON 体），状态码白名单，可选响应体关键串
- 探测成功的模型列表照旧写进 @@gateway_manager_upstream_models@@ 与 @@/v1/models@@ 聚合
- 每 @@health_interval_seconds@@ 秒跑一次；控制台「上游」页显示最近结果与错误

## 15. 成功请求采样（新）

- @@server.sample_every = N@@：**每 N 个成功请求**留 1 条（0 关闭），保留 @@sample_limit@@ 条
- 留存内容与拦截回放一致：端点、模型、状态码、上游、耗时、脱敏头、请求体（>8 KB 截断）
- 控制台「规则」页「成功请求采样」表：点 **复现** 用当前规则链重跑，看改动后是否行为变化（回归验证）
- 接口：@@GET|DELETE /admin/api/samples@@、@@POST /admin/api/samples/test {id}@@
- 只在内存里，进程重启即清空

## 16. 规则书 DSL（新）

拖拽 UI 与纯文本双向同步，复杂规则用文本更快：

@@@yaml
- id: r-tag
  name: 打标记
  type: header
  action:
    header_name: X-Gateway
    header_value: gateway-manager

- id: r-deny-danger
  name: 拦截危险模型
  type: deny
  when:
    models: danger, blocked
    paths: /v1/chat/completions
    header: X-Client=android
    body_contains: "messages":[]
  action:
    status: 451
    message: blocked by rule book
@@@

| 段 | 字段 |
| --- | --- |
| 顶层 | @@id@@ / @@name@@ / @@type@@（match·inject·override·header·deny）/ @@disabled@@ |
| when | @@models@@（逗号）/ @@paths@@（逗号）/ @@header: Key=Value@@ / @@body_contains@@ |
| action | @@instruction@@ / @@profile_id@@ / @@model@@ / @@params@@（JSON）/ @@header_name@@ / @@header_value@@ / @@status@@ / @@message@@ |

- 控制台「规则」页展开「文本模式」：@@从当前规则生成@@ → 编辑 → @@校验@@（只解析不改）→ @@解析并应用@@
- 解析失败会带行号，例如 @@第 3 行：when 里未知字段 "nope"@@
- 接口：@@GET /admin/api/rules/dsl@@、@@POST /admin/api/rules/dsl {text}@@、@@POST /admin/api/rules/dsl/preview {text}@@
- 应用前依旧自动留配置快照

## 17. 多实例联邦（新）

@@@json
"federation": {
  "enabled": true,
  "timeout_ms": 3000,
  "peers": [
    { "id": "edge-a", "name": "A 机房", "base_url": "http://10.0.0.11:7878", "token": "<对端 admin_token>" }
  ]
}
@@@

- 控制台「状态」页顶部「联邦总览」：并行拉取每个 peer 的 @@/admin/api/status@@，汇总版本、运行时长、请求数、规则拦截数、上游(异常)、规则数、延迟
- 单点故障不影响整体：失败的 peer 单独打红并显示错误原因
- 接口：@@GET /admin/api/federation@@
- 说明：只做**只读汇总**，不会跨实例转发代理请求；跨实例调度请用上游指向对端 @@/v1@@

## 18. 上游额度预算（新）

@@@json
"upstreams": [
  { "id": "remote", "base_url": "https://api.example.com",
    "budget": { "max_requests_per_day": 2000, "max_tokens_per_day": 500000 } }
]
@@@

- 按自然日（本地时区）计数，跨天自动清零
- 请求数精确计数；token 是**估算值**（请求体字节 / 4），只作预算护栏
- 用尽后该上游被跳过（日志和响应里会说明「日请求预算已用尽」），其它上游照常
- 观测：@@gateway_manager_budget_{requests,tokens}_{used,limit}@@、@@gateway_manager_budget_exhausted@@，控制台「上游」页也显示
- 注意：预算只在内存里计数，重启归零（避免旧数据误伤）

## 19. 批量回归（新）

- 控制台「规则」页「批量回归」：来源可选 @@采样 + 回放@@ / @@只用采样@@ / @@只用回放@@，条数上限 500
- 输出：总计 / 拦截 / 放行、每条规则的命中次数、以及逐条明细（来源、模型、路径、结果、命中规则）
- 接口：@@POST /admin/api/rules/batch@@，body 支持 @@{from: "samples"|"replay"|"both", limit}@@ 或直接给 @@{requests:[{model,path,body}]}@@
- 典型用法：改完规则书 → 跑一次批量回归 → 拦截率异常升高就立刻回滚快照

## 20. 跨实例转发（新）

把联邦 peer 声明成上游，就能在一个入口后面调度多台实例：

@@@json
"federation": {
  "enabled": true,
  "peers": [
    { "id": "edge-a", "base_url": "http://10.0.0.11:7878",
      "as_upstream": true, "access_key": "<对端 access_keys 之一>",
      "models": [], "priority": 5, "weight": 1 }
  ]
}
@@@

- 生成的上游 ID 是 @@peer-<id>@@，参与优先级排序、权重灰度、冷却、换 key、预算（若 peer 自己配了预算则走对端）
- 控制台「上游」页能看到 @@peer-*@@ 的请求/成功/失败与延迟
- 典型拓扑：边缘机跑策略注入 → 中心机跑真实供应商 key，入口指向边缘机；或反过来做异地容灾

## 21. token 精确计量（新）

- 非流式响应：解析 @@usage.total_tokens@@（OpenAI）或 @@usage.input_tokens + output_tokens@@（Anthropic），最多缓冲 2 MB，超限则退回流式不计量
- 流式响应：保留最近 256 KB 尾部窗口，结束后解析最后一个 @@usage@@（大部分供应商在末块给）
- 流程：请求前按 @@字节/4@@ 预扣 → 拿到真实 usage 后 **纠正**预算余额（@@ReconcileBudget@@）
- 指标：@@gateway_manager_tokens_total{upstream,name}@@ 累计真实 token；@@gateway_manager_budget_tokens_used@@ 是纠正后的预算余额占用

## 22. 上游适配器（新）

用配置描述厂商差异，不改代码：

@@@json
"adapters": [
  {
    "id": "anthropic-style",
    "match_base_url": "https://api.anthropic.com",
    "messages_path": "/v1/messages",
    "models_path": "/v1/models",
    "auth_header": "x-api-key",
    "auth_prefix": "",
    "extra_headers": { "anthropic-version": "2023-06-01" }
  }
]
@@@

- 按 @@match_base_url@@ 前缀最长匹配生效
- 覆盖：聊天/Responses/Messages 端点路径、模型列表路径、认证头名与前缀、额外请求头
- 健康探测也走适配器的 @@models_path@@（除非上游自己配了 @@probe@@）

## 23. 条件表达式（新）

规则条件支持表达式，比字段匹配更灵活：

@@@yaml
- id: r-expr-deny
  name: 正则拦截
  type: deny
  when:
    expr: model ~ "^danger-" and path == "/v1/chat/completions"
  action:
    status: 451
    message: regex blocked
@@@

| 语法 | 说明 |
| --- | --- |
| @@model == "x"@@ / @@!=@@ | 精确比较 |
| @@model ~ "^(gpt|claude)-"@@ / @@!~@@ | 正则匹配 |
| @@body contains "text"@@ | 子串包含 |
| @@header:X-Client == "android"@@ / @@header["X-Client"]@@ | 读请求头 |
| @@a and b@@ / @@a or b@@ | 组合（and 优先级更高） |
| @@!(...)@@ / @@!expr@@ | 取反，支持括号分组 |

- 与 @@models@@/@@paths@@/@@body_contains@@ 等字段是 **AND** 关系
- 保存配置时先做语法校验，非法表达式（重复运算符、坏正则、未知字段）直接 400
- 控制台规则编辑器里也有「条件表达式」输入框，DSL 里写 @@expr: ...@@

## 24. 分流镜像（新）

@@@json
"server": {
  "mirror_enabled": true,
  "mirror_upstream": "shadow",
  "mirror_ratio": 10,
  "mirror_models": ["gpt-4o-mini"],
  "mirror_timeout_ms": 10000
}
@@@

- 主请求照常返回，后台按比例把**同一份请求体**（已注入）复制给影子上游，忽略响应内容
- 影子投递不占用主链路延迟；影子自己的 token 用量照常计入预算与 @@gateway_manager_tokens_total@@
- 指标：@@gateway_manager_mirror_total{upstream,name,result="ok|fail"}@@
- 典型用法：新上游/新策略上线前，先 10% 影子对照，看延迟与错误率

## 25. 快照 Git 化（新）

@@@json
"server": { "snapshot_git": true, "snapshot_dir": "" }
@@@

- 开启后每次创建快照都会在快照目录里 @@git init@@ + @@git add -A@@ + @@commit@@（首次自动初始化，内置 @@user.name/email@@，不依赖全局配置）
- 接口：@@GET /admin/api/snapshots/git/log?limit=20@@（哈希 / 时间 / 说明）、@@POST /admin/api/snapshots/git/commit {message}@@
- 控制台「配置」页「快照 Git 历史」表，可一键手动提交
- 机器上没有 git 时自动降级：照常存快照，只是不提交

## 26. 熔断半开（新）

上游连续失败达到 @@failure_threshold@@ 后进入 **open**（冷却），冷却到期不是直接放全量，而是转入 **half_open**：

@@@
closed ──失败≥阈值──▶ open ──冷却到期──▶ half_open ──探测成功──▶ closed
                          ▲                    │
                          └────探测失败◀────────┘（冷却 × 2 指数退避，上限 cooldown_max_seconds）
@@@

- @@half_open_limit@@ 控制半开时允许的并发探测数（默认 1）
- 退避：第 n 次打开冷却 = @@min(base × 2^(n-1), cooldown_max_seconds)@@
- 状态可见：@@/admin/api/status@@ 每个上游的 @@circuit@@ 字段；控制台「上游」表新增「熔断」列
- 指标：@@gateway_manager_upstream_circuit{upstream,name,state}@@

## 27. A/B 实验（新）

@@@json
"experiments": [
  { "id": "exp-1", "name": "策略对照", "models": ["gpt-4o-mini"],
    "arms": [
      { "id": "arm-a", "name": "新策略", "weight": 1, "profile_id": "coder", "upstream_id": "remote" },
      { "id": "arm-b", "name": "旧策略", "weight": 1, "upstream_id": "local-gw" }
    ] }
]
@@@

- 按 @@models@@ 子串匹配实验；命中后按 @@weight@@ 分臂（计数器取模，确定性）
- 每臂可指定 @@profile_id@@（覆盖策略选择）和 @@upstream_id@@（把候选上游限定为该上游）
- 自动统计：次数 / 错误数 / 累计延迟 / 累计 token
- 控制台「状态」页「A/B 实验」表；接口 @@GET /admin/api/experiments@@
- 指标：@@gateway_manager_experiment_total{experiment,arm,result}@@、@@gateway_manager_experiment_{latency_ms,tokens}_sum@@

## 28. 一键安装（新）

@@@bash
# Linux（root）
./install.sh                                   # 默认 build/gateway-manager-linux-amd64 → /opt/gateway-manager + systemd
sudo PORT=7878 BIND=0.0.0.0 ACCESS_KEY=sk-local ./install.sh
sudo ./install.sh --uninstall
@@@

@@@powershell
# Windows（管理员 PowerShell）
.\install.ps1                                  # 默认 build\gateway-manager.exe → %LOCALAPPDATA%\gateway-manager
.\install.ps1 -Port 7878 -AccessKey sk-local -AdminToken admin -Service
.\install.ps1 -Uninstall
@@@

- 两个脚本都会：复制二进制 → 生成/修正配置（端口、绑定、key、admin token）→ 可选注册服务 → 打印控制台与指标地址
- install.sh 自动创建系统用户 @@gateway-manager@@、写 systemd unit（含 @@ProtectSystem@@ 等加固）、@@systemctl enable --now@@
- install.ps1 用原生 @@ConvertFrom-Json@@ 改配置，@@sc.exe@@ 注册服务并设置失败自动重启；文件带 UTF-8 BOM 以免 PowerShell 5.1 按 ANSI 解析中文

## 29. Docker 与一键可观测栈（新）

```bash
docker compose up -d     # gateway-manager + Prometheus + Grafana 一起起来
# 控制台     http://127.0.0.1:7878/ui
# 指标       http://127.0.0.1:7878/metrics
# Prometheus http://127.0.0.1:9090
# Grafana    http://127.0.0.1:3000 （admin/admin，看板已自动导入）
```

- 镜像：`Dockerfile` 多阶段构建（`golang:1.21-alpine` → `alpine:3.20`），非 root 用户、只读根文件系统、`no-new-privileges`、内置 `HEALTHCHECK`
- 首次启动自动 `init` 生成配置，并通过环境变量修正监听地址：`GATEWAY_MANAGER_BIND` / `GATEWAY_MANAGER_PORT` / `GATEWAY_MANAGER_ACCESS_KEY` / `GATEWAY_MANAGER_ADMIN_TOKEN`
- 数据卷 `./data`：配置、状态、快照都在里面
- `docker/prometheus.yml` 抓 `gateway-manager:7878`；`deploy/prometheus-alerts.yml` 直接挂进 Prometheus；`deploy/grafana-dashboard.json` 由 provisioning 自动导入
- 单独构建：`docker build -t gateway-manager . && docker run -d -p 7878:7878 -v "$PWD/data:/data" gateway-manager`

## 30. 反代渠道（新）

「渠道」就是你真正要反代的**模型来源**：地址 / 路径 / 认证方式 / 额外请求头都取自原版 APK 的反编译结果，填上凭证即可当上游用。

| 模板 id | 名称 | 端点 | 认证 | 状态 |
| --- | --- | --- | --- | --- |
| `opencode-zen` | OpenCode Zen（匿名免费档） | `opencode.ai/inference/v1/chat/completions` | Bearer（填 `public`） | ✅ openai |
| `zhipu-autoclaw` | 智谱 AutoGLM / autoclaw | `autoglm-acceleration-api.zhipuai.cn/autoclaw-proxy/proxy/autoclaw/chat/completions` | `X-Authorization: Bearer` | ✅ openai |
| `workbuddy` | WorkBuddy / CodeBuddy | `www.workbuddy.ai/v2/chat/completions` | Cookie | ✅ openai |
| `raccoon` | 商汤 小浣熊 | `xiaohuanxiong.com/api/web/llm/v2/chat/completions` | `X-Auth-Token` | ✅ openai |
| `xunfei-loomy` | 科大讯飞 Loomy | `loomyad.xunfei.cn/api/v1/chat/completions` | 查询参数 `token` | ✅ openai |
| `deepseek-web` | DeepSeek 网页版 | `chat.deepseek.com/api/v0/chat/completion` | Bearer + cookie | ✅ 已适配（PoW + patch 流） |
| `tabbit` | Tabbit | `web.tabbit.com/api/v3/chat/runs` | Cookie + 签名头 | ⚠️ 待适配 |
| `catpaw` | 美团 CatPaw | `ai.catpaw.meituan.com` | Cookie | ⚠️ 待适配 |
| `dangbei` | 当贝 AI | `ai-api.dangbei.net/ai-search` | Bearer | ⚠️ 待适配 |
| `runable` | Runable | `api.runable.com/api/chat/text` | Cookie | ✅ 已适配（AI SDK 流） |
| `meoo` | 阿里 秒悟 Meoo | `meoo.com/api/v1/...` | Cookie | ⚠️ 待适配 |
| `wenxin` | 百度 文心 | `chat.baidu.com/aichat/api/conversation` | Cookie | ⚠️ 待适配 |

### 网页登录（推荐）

不用手动扒 cookie：点「网页登录」，工具会用本机 Chrome / Edge 打开厂商登录页（独立 profile，不影响你自己的浏览器），登录完成后自动把该域名的 cookie 与页面存储抓回来，一键保存成渠道凭证。

```
控制台「渠道」→ 选模板 → 网页登录 → 在弹出的浏览器里登录 → 面板显示「已抓到凭证」→ 保存
```

- 原理：以 `--remote-debugging-port` 启动 Chrome/Edge，通过 DevTools 协议读取 `Storage.getCookies` 与页面 `localStorage/sessionStorage`（纯标准库实现的 WebSocket 客户端，无额外依赖）
- 凭证优先级：指定 `token_key`（页面存储里的键）→ 自动挑最像 token 的键 → 都没有才用 cookie 串
- 需要本机有 Chrome / Edge / Chromium；没有时会明确提示「改用手动粘贴」
- 接口：`POST /admin/api/channels/login/start`、`GET /admin/api/channels/login/status`、`POST /admin/api/channels/login/finish`、`POST /admin/api/channels/login/cancel`

### 用法

1. 控制台「渠道」页 → 选一个模板点「加入」→ 粘贴凭证（token / cookie；`opencode-zen` 填 `public`）
2. 保存后它自动变成一条上游：ID 为 `ch-<渠道 id>`，参与优先级 / 权重 / 冷却 / 预算 / 指标
3. 点「探活」用该模板的模型端点做一次连通性检查（无模型端点的模板会提示跳过）
4. 客户端照旧 `http://127.0.0.1:7878/v1`，模型名用该渠道的模型即可

也可以直接写配置：

```json
"channels": [
  { "id": "zhipu", "preset": "zhipu-autoclaw", "token": "<从桌面端登录态取的 token>",
    "enabled": true, "priority": 0, "weight": 0,
    "base_url": "https://autoglm-acceleration-api.zhipuai.cn",
    "models": ["glm-5"], "headers": { "X-Request-Model": "glm-5" } }
]
```

### DeepSeek 适配细节（已打通）

DeepSeek 网页版不是 OpenAI 形状，三步都补上了：

1. **PoW（DeepSeekHashV1）**：`POST /api/v0/chat/create_pow_challenge` 拿到 `challenge/salt/expire_at/difficulty`，然后暴力找 `n` 使 `hash(salt_expire_at_ + n)` 等于 challenge。这里的关键是：它**不是标准 SHA3-256**，而是原版 APK 里那份 Keccak-f1600 的变体——**23 轮（跳过 RC[0]）+ rate 136 + 0x06/0x80 padding**。照标准 SHA3 算永远解不出来（实测 144000 次内 0 命中，改对轮数后 72648 命中）。解出后 base64 成 JSON 放进 `x-ds-pow-response`。
2. **prompt 格式**：OpenAI messages 拼成 `<｜User｜>...<｜Assistant｜>...<｜end▁of▁sentence｜>...<｜System｜>...<｜Assistant｜>`。
3. **响应还原**：DeepSeek 的 patch 流（`{"v":{...}}` 首帧快照、`{"v":"增量"}`、`{"p":...,"o":"APPEND","v":"..."}`、`BATCH` 里的 `accumulated_token_usage`）转回 OpenAI 的 `chat.completion(.chunk)`，流式带 `[DONE]`，用量写进 `usage.completion_tokens`。

实测（用自己的登录态）：

```
非流式 → {"choices":[{"message":{"content":"我是DeepSeek，由深度求索公司创造的AI助手…"}}],
          "usage":{"completion_tokens":64,"total_tokens":64}}
流式   → 21 个 chunk，首帧 role、增量 delta、末帧 finish_reason=stop、usage、[DONE]
```

### 渠道适配器注册表

需要协议转换的渠道在 `channels.go` 的 `channelAdapters` 里登记一行，实现形如：

```go
"runable": (*Server).runableForward,
```

`forward()` 会先看渠道有没有专属适配器：有就走协议转换（OpenAI 进、OpenAI 出），没有就走通用 OpenAI 透传。DeepSeek 与 Runable 就是按这个方式接进来的，后面每打通一家只需加一个文件 + 一行注册。

### 网页登录打不开怎么办

v2.5.3 起已经处理了最常见的坑：

| 现象 | 原因 / 修法 |
| --- | --- |
| 报「连接浏览器调试端口失败 … refused」 | profile 目录里残留了上次的 `DevToolsActivePort`（旧端口），现在启动前会自动删除；若仍失败会自动换一个全新 profile 重试 |
| 浏览器已经在开着 | 现在会先尝试复用已有调试端口，连不上才启动新实例，不再和已运行实例打架 |
| 报「未找到 Chrome / Edge / Chromium」 | 检查是否装了 Edge/Chrome，或改用手动粘贴凭证 |
| 报「浏览器没有打开调试端口（20 秒超时）」 | 现在会带上浏览器 stderr 片段与 profile 路径，便于定位（常见于安全软件拦截） |

### 凭证与边界

- 凭证存在本地 `config.json`（**不会**上传到任何地方）；`/admin/api/channels` 只返回遮罩值（`tk…mo`）
- 不内置自动登录 / 抓 cookie：凭证需要你自己从合法持有的账号里取
- 标 ⚠️ 的模板只做了地址与请求头登记，请求/响应协议仍需适配（Tabbit 需要签名头，其余需要各自私有响应格式）
- 接非标准协议时也可以用 `adapters`（路径 / 认证头 / 额外头）或自己在 `channels.go` 加一个 adapter 函数

## 31. 配置快照与回滚（新）

- 目录：@@<config 同目录>/snapshots/@@（可用 @@server.snapshot_dir@@ 改），文件名形如 @@20261029-212637-before-config-update.json@@
- 自动留档点：保存配置、保存规则、应用模板、回滚前（受 @@snapshot_before_save@@ 控制）；也可手动 @@新建快照@@ 并写备注
- 保留 @@snapshot_limit@@ 版（默认 20），超出删最旧；快照就是完整配置文件，回滚 = 载入 + 校验 + 落盘 + 热加载
- 控制台「配置」页：列表（时间 / 大小 / 上游 / 策略 / 规则 / 名称）+ @@查看@@（只填进编辑器，不生效）/ @@回滚@@ / @@删除@@
- 接口：@@GET /admin/api/snapshots@@、@@POST /admin/api/snapshots/create@@、@@/snapshots/rollback@@、@@/snapshots/delete@@、@@GET /admin/api/snapshots/get?name=@@；快照名做了正则白名单，@@../@@ 之类直接 400

## 32. 拦截回放（新）

- 每次被 @@deny@@ 规则拦下的请求都会留档：时间、方法、路径、模型、状态码、命中的规则、**脱敏后的请求头**、请求体（超过 8 KB 截断并标记）
- 敏感头（authorization / api-key / token / cookie / secret）一律存成 @@***@@
- 控制台「规则」页的「拦截回放」表：点 **复现** 会用**当前**规则链重新跑一遍，直接看到"会不会仍被拦 / 命中哪些节点 / 会注入什么 / 最终请求体"——改完规则立刻验证效果
- 接口：@@GET|DELETE /admin/api/replay@@、@@POST /admin/api/replay/test {id}@@
- 保留条数 @@server.replay_limit@@（默认 30，0 关闭）；只存最近 N 条，进程重启即清空（不落盘，避免把用户请求长期留在磁盘）

## 33. 接口一览

鉴权：@@admin_token@@ 为空时开放；非空时需要 @@X-Admin-Token: <token>@@、@@Authorization: Bearer <token>@@ 或 @@?token=@@。


| 路径 | 说明 |
| --- | --- |
| `GET /` / `GET /health` | 服务信息 / 健康检查（不鉴权） |
| `GET /v1/models`、`/models` | 聚合模型 + 别名 |
| `POST /v1/chat/completions`（`/chat/completions`） | 注入 `messages[0] = system` |
| `POST /v1/responses`（`/responses`）、`/v1/responses/compact` | 注入 `instructions` |
| `POST /v1/messages`（`/messages`） | 注入 `system`（字符串拼接或数组前插） |
| `GET /ui` | 控制台 |
| `GET /admin/api/status` | 状态 + 上游 + key 池 + 规则 + 统计 |
| `GET /admin/api/logs?limit=200` | 日志 |
| `GET|POST /admin/api/config` | 读 / 写配置（热加载） |
| `POST /admin/api/reload` | 从磁盘重载 |
| `POST /admin/api/check` | 立即健康检查 |
| `GET|POST /admin/api/rules` | 读 / 保存规则链 |
| `POST /admin/api/rules/test` | 规则 dry-run |
| `GET|POST /admin/api/templates` / `templates/apply` / `templates/delete` | 模板库 |
| `GET|DELETE /admin/api/rules/stats` | 规则命中统计（读 / 清零） |
| `GET /metrics` | Prometheus 指标（含直方图） |
| `GET /admin/api/snapshots` / `snapshots/create` / `snapshots/rollback` / `snapshots/delete` / `snapshots/get` | 配置快照 |
| `GET /admin/api/snapshots/git/log`、`POST /admin/api/snapshots/git/commit` | 快照版本库 |
| `GET|DELETE /admin/api/replay`、`POST /admin/api/replay/test` | 拦截回放（读 / 清空 / 复现） |
| `GET|DELETE /admin/api/samples`、`POST /admin/api/samples/test` | 成功请求采样 |
| `GET /admin/api/rules/dsl`、`POST /admin/api/rules/dsl`、`POST /admin/api/rules/dsl/preview` | 规则书 DSL |
| `POST /admin/api/rules/batch` | 批量回归（采样/回放/内联请求） |
| `GET /admin/api/federation` | 多实例联邦汇总 |
| `GET /admin/api/experiments` | A/B 实验配置与统计 |

## 34. 部署

```bash
# Linux（systemd）
sudo install -d -o gateway-manager -g gateway-manager /opt/gateway-manager
sudo cp build/gateway-manager-linux-amd64 /opt/gateway-manager/ && sudo cp config.json /opt/gateway-manager/
sudo cp deploy/gateway-manager.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now gateway-manager
```

```powershell
# Windows（服务常驻，管理员）
.\deploy\install-windows-service.ps1 -ExePath D:\gateway-manager\gateway-manager.exe -ConfigPath D:\gateway-manager\config.json
```

需要跨机访问时先设 `access_keys` + `admin_token`，再把 `bind` 改成 `0.0.0.0`，并建议放在 Nginx / Caddy 后面加 TLS。

## 35. 构建与验证

```bash
go vet ./...        # 0 问题
go test ./... -v    # 47/47 PASS
./build.ps1         # Windows 出 7 个平台产物
./build.sh          # Linux/macOS 同上
```

| 产物 | 平台 | 大小 | 格式 |
| --- | --- | --- | --- |
| build/gateway-manager-windows-amd64.exe | windows/amd64 | 7.46 MB | PE |
| build/gateway-manager-windows-arm64.exe | windows/arm64 | 6.77 MB | PE |
| build/gateway-manager-linux-amd64 | linux/amd64 | 7.25 MB | ELF |
| build/gateway-manager-linux-arm64 | linux/arm64 | 6.75 MB | ELF |

21 个用例：注入 ×3 形态、failover、鉴权、SSE 合流、SSE 透传、模型聚合、别名与策略选择、路径规范化、key 池打分与冷却、401 换 key 重试、规则拦截、规则注入优先于策略、规则改写参数与请求头、规则条件匹配、模板库增删查、规则 dry-run 接口、模板应用到策略、**key 池导出恢复**、**状态文件往返（无明文 key）**、**规则命中统计接口与清零**、**Prometheus 指标端点（含量值断言）**、**快照创建/自动留档/回滚/路径穿越拒绝**、**直方图桶与 +Inf 计数**、**回放记录与脱敏/复现**、**Grafana 看板 JSON 结构校验**、**权重灰度分流比例（1:9→2:18）**、**耗时三段直方图**、**告警规则文件结构**、**自定义探测路径与 expect_contains**、**采样留档与复现**、**规则书往返与行号报错**、**日预算用尽与指标**、**联邦并行汇总（含离线 peer）**、**批量回归统计**、**usage 纠正预算 + token 指标（含 SSE）**、**peer 当上游转发**、**适配器路径/认证/额外头**、**表达式语法与拦截**、**镜像投递与指标**、**快照 git 提交**、**熔断半开与指数退避**、**实验分臂与统计**、**安装脚本结构（含 BOM 断言）**。

运行时实测（Windows 127.0.0.1:17880 + 假上游 18081）：

```
chat#1   key 顺序 sk-bad → sk-good，最终 200（401 后自动换 key）
chat#2   mini 命中 inject 规则 → 上游 messages[0] = {"role":"system","content":"答案 3 句以内"}
chat#3   cheap 命中 override 规则 → model=fake-large, temperature=0.2, 附带 X-Gateway=gateway-manager
key 池   sk…od 98(ok) / sk…lt 80(ok) / sk…ad 20(bad)   ← 坏 key 被降权
dry-run  matched=[r-tag,r-mini] skipped=[r-param] denied=false
模板     总数 13（内置 12 + 自定义 1），应用后策略数 2（p1 + from-tpl）
规则保存 后 POST model=bad → 403 {"message":"no bad model"}
UI       33.5 KB，含规则编辑器 / 模板库 / 拖拽
```

Linux 实测（WSL2，内核 6.18.54.1-microsoft-standard-WSL2 x86_64）：`version / init / serve / health / 12 个内置模板 / 3 个规则节点 / key 池字段 / UI 拖拽标记` 全部正常。

## 36. 目录结构

```
gateway-manager/
|-- main.go              CLI：serve / discover / check / init / dump / version
|-- config.go            配置结构、默认值、校验、原子落盘、示例配置
|-- strategy.go          端点类型、注入规则、策略选择、模型解析
|-- rules.go             规则模型、条件匹配、规则链求值、校验
|-- keys.go              key 池、健康打分、冷却、指纹与快照
|-- templates.go         内置 + 自定义策略模板
|-- server.go            路由、鉴权、代理主循环、换 key/换上游、SSE
|-- state.go             运行时状态、冷却/限流/日志环/统计
|-- models.go            /v1/models 聚合
|-- health.go            后台健康检查
|-- discover.go          本机端口扫描 + 模型探测
|-- metrics.go           Prometheus /metrics 文本输出
|-- snapshots.go         配置快照：创建 / 列表 / 回滚 / 清理
|-- histogram.go         延迟直方图（12 桶 + Inf）
|-- rules_dsl.go         规则书 DSL 解析 / 序列化
|-- federation.go        多实例联邦汇总（并行拉取 peer 状态）
|-- usage.go             响应 usage 解析（JSON / SSE 尾窗口）
|-- expr.go              规则条件表达式求值
|-- git.go               快照的 git 提交与历史
|-- install.sh            Linux 一键安装
|-- install.ps1           Windows 一键安装
|-- webui.go             控制台页面（规则拖拽 / 热力 / 模板库 / key 池 / 快照）
|-- webui_handlers.go    /admin/api/* 处理器
|-- jsonutil.go          小工具
|-- gateway_test.go      18 个用例
|-- build.ps1 / build.sh 交叉编译
|-- deploy/              systemd unit、Windows 服务脚本、Grafana 看板、Prometheus 抓取与告警规则
```

## 37. 边界

- 只做本机代理与注入治理，不内置第三方账号登录 / Cookie 抓取 / 私有接口适配
- 暴露到局域网前务必设 `access_keys` 与 `admin_token`
- key 池的分与罚只在本进程内存里，重启后回到初始 80 分
