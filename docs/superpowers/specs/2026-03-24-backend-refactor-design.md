# 后端可读性重构设计（强类型优先）

日期：2026-03-24  
范围：仅 Go 后端，不改前端 `index.html`

## 1. 目标与约束

### 1.1 目标
- 先建立强类型模型，再完成模块拆分，消除核心路径上的 `map[string]any`。
- 在不改变接口路径的前提下显著提升可读性、可维护性和可测试性。
- 为后续迭代（更多测试、性能优化）打下结构基础。

### 1.2 约束
- 允许小幅行为修正（如更规范的错误码/错误文案）。
- 保持现有接口路径不变：
  - `GET /`
  - `GET /config.json`
  - `POST /save_config`
  - `GET /news_list`
  - `POST /delete_news`
  - `POST /run_query`
  - `POST /parse_time`
  - `GET /logs`
  - `POST /add_log`
- 测试范围选择：仅纯函数/解析逻辑，不做 handler 级 `httptest`。

## 2. 目标目录与分层

```text
news_query_system/
├── cmd/server/main.go
├── internal/app/app.go
├── internal/http/handlers/
│   ├── config_handler.go
│   ├── news_handler.go
│   ├── query_handler.go
│   ├── timeparse_handler.go
│   └── logs_handler.go
├── internal/domain/
│   ├── config.go
│   ├── news.go
│   ├── log.go
│   └── timeparse.go
├── internal/service/
│   ├── query_service.go
│   ├── timeparse_service.go
│   └── news_service.go
├── internal/store/
│   ├── config_store.go
│   └── log_store.go
├── internal/integrations/ark/client.go
├── internal/platform/logger/logger.go
├── internal/platform/httpx/response.go
└── internal/util/
```

### 2.1 分层职责
- `handlers`：请求解析、参数校验、调用 service、统一响应。
- `service`：业务编排，不处理 HTTP 细节。
- `store`：`config.json`、`logs.json` 持久化。
- `integrations`：Ark API 协议、请求和响应解析。
- `domain`：跨层共享强类型模型。
- `platform`：日志与响应等通用基础设施。

## 3. 强类型模型设计

## 3.1 配置模型
- `Config`
  - `Themes []Theme`
  - `Settings Settings`
- `Theme`
  - `ID string`
  - `Name string`
  - `Prompt string`
  - `Enabled bool`
  - `Folder string`
  - `Hour *int`
  - `Minute *int`
  - `MinNewsCount int`
- `Settings`
  - `OutputBasePath string`
  - `QueryTime string`
  - `CronSchedule string`

## 3.2 请求/响应模型
- `RunQueryRequest { ThemeID string }`
- `RunQueryResponse`
  - `Success bool`
  - `Output string`
  - `Message string`
  - `Error string`
  - `Details string`
  - `ActualCount int`
  - `MinCount int`
- `ParseTimeRequest { Description string }`
- `ParseTimeResponse`
  - `Success bool`
  - `Cron string`
  - `Hour *int`
  - `Minute *int`
  - `Display string`
  - `Error string`
  - `RawOutput string`

## 3.3 兼容策略
- 读取配置时采用宽松反序列化，兼容旧格式（例如 `id` 可能是 number）。
- 落盘统一输出新格式（`id` 为 string）。
- 对外 JSON 字段命名保持现有协议，降低前端改动风险。
- 错误响应统一为 `{success:false,error,details?}`，允许小幅文案和状态码优化。

## 4. 实施步骤与回滚点

## 4.1 建立新入口与骨架
- 新增 `cmd/server/main.go`、`internal/app/app.go`，迁移服务初始化与路由注册。
- 回滚点：服务可启动，`GET /` 正常。

## 4.2 迁移配置与日志存储
- 新建 `domain` 与 `store`，替换配置/日志核心路径的动态 map。
- 回滚点：
  - `GET /config.json`
  - `POST /save_config`
  - `GET /logs`
  - `POST /add_log`
  行为可用。

## 4.3 迁移 `/parse_time`
- `handlers/timeparse_handler.go` + `service/timeparse_service.go` + `integrations/ark/client.go`。
- 保留现有解析语义与回退逻辑（responses -> chat/completions）。
- 回滚点：`POST /parse_time` 返回结构兼容。

## 4.4 迁移 `/run_query`、`/news_list`、`/delete_news`
- 抽离新闻扫描、查询编排、文件删除逻辑到 service。
- 保留最少新闻条数检查与输出路径行为。
- 回滚点：手工执行一次 `run_query` 成功，新闻列表与删除可用。

## 4.5 清理与收敛
- 清理旧 `main.go` 冗余逻辑。
- 统一日志与响应封装。
- 回滚点：所有既有接口 smoke 通过。

## 5. 测试策略（纯函数/解析）

## 5.1 覆盖范围
- ID 归一化与字符串/数字兼容转换。
- cron 文本提取与 `hour/minute` 推导。
- `extractTextFromResponses` 多分支解析。
- `extractTextFromChat` 边界分支。
- `countNewsInFile` 在不同 HTML 结构下的计数行为。
- 错误映射规则（如 `ERROR:` 前缀、timeout 文案）。

## 5.2 不覆盖范围
- handler 级别 HTTP 集成测试。
- 外部依赖（`opencode`、Ark API）的端到端调用。

## 6. 验收标准

1. 后端完成分层，入口不再承载业务细节。  
2. 核心路径（配置、查询、时间解析）完成强类型化。  
3. 既有接口路径保持不变。  
4. `go test ./...` 通过（纯函数/解析测试）。  
5. 手工 smoke：`GET /`、`GET /config.json`、`POST /parse_time`、`POST /run_query` 可用。

## 7. 风险与缓解

- 风险：一次性类型化导致兼容性回归。  
  缓解：分批迁移并保留每步回滚点。  
- 风险：`/run_query` 外部命令依赖导致验证不稳定。  
  缓解：将可测部分下沉为纯函数，外部调用边界保持薄层封装。  
- 风险：历史配置格式混杂。  
  缓解：读取宽松、写入收敛，并在日志中记录兼容转换行为。
