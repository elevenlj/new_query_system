# 新闻查询配置系统

一个基于 Go 后端 + Web 前端的新闻查询配置系统，支持多主题管理、手动触发查询、前端定时执行和时间描述解析（cron）。

## 功能

- 多主题配置（名称、提示词、输出目录、最少新闻数）
- 查询前一天新闻并输出 HTML
- Web 界面管理配置
- 前端定时器按主题时间执行
- 自然语言时间解析为 cron 表达式（Ark Agent API）
- 操作日志与服务日志记录

## 项目结构

```text
news_query_system/
├── main.go              # Go 服务端
├── go.mod               # Go 模块定义
├── index.html           # Web 配置页面
├── config.json          # 配置文件
├── logs.json            # 操作日志
├── logs/                # 服务日志目录
└── news_output/         # 新闻输出目录
```

## 启动

```bash
cd /Users/bytedance/Desktop/develop/ai/news_query_system
GOCACHE=/tmp/go-build go run main.go
```

默认监听 `http://localhost:8000`。

可选指定端口：

```bash
PORT=18000 GOCACHE=/tmp/go-build go run main.go
```

## 使用方式

1. 打开 `http://localhost:8000`
2. 添加或编辑新闻主题
3. 点击“立即查询测试”手动执行
4. 或点击“启动定时器”由前端按主题时间执行

## 配置说明

`config.json` 主要字段：

- `themes`: 主题数组
  - `id`: 主题 ID
  - `name`: 主题名称
  - `prompt`: 查询提示词
  - `enabled`: 是否启用
  - `folder`: 输出子目录
  - `hour` / `minute`: 主题执行时间（可选）
  - `min_news_count`: 最少新闻条数（可选）
- `settings`
  - `output_base_path`: 输出根目录
  - `query_time` / `cron_schedule`: 兼容字段

## 接口

- `GET /`：前端页面
- `GET /config.json`：读取配置
- `POST /save_config`：保存配置
- `GET /news_list`：读取新闻文件列表
- `POST /run_query`：执行单主题查询
- `POST /parse_time`：时间描述解析（返回 cron）
- `GET /logs`：读取操作日志
- `POST /add_log`：写入操作日志
- `POST /delete_news`：删除新闻文件

## 日志

- 服务日志：`logs/server-YYYY-MM-DD.log`
- 操作日志：`logs.json`

`/parse_time` 链路包含输入、模型请求、模型原始输出、解析结果、耗时和 `trace_id`，便于排查。

## 依赖

- Go 1.24+
- `opencode` 命令（用于 `/run_query` 查询执行）

## 常见问题

1. 页面提示无法连接后端：确认服务由 `go run main.go` 启动，端口与访问地址一致。
2. 查询失败：检查机器是否可执行 `opencode`，并查看 `logs/server-YYYY-MM-DD.log`。
3. 时间解析失败：查看服务日志中 `[parse_time][trace_id]` 相关条目。
# new_query_system
# new_query_system
