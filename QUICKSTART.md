# 快速开始（Go 版）

## 1. 启动服务

```bash
cd /Users/bytedance/Desktop/develop/ai/news_query_system
GOCACHE=/tmp/go-build go run main.go
```

看到启动日志后，打开：

- `http://localhost:8000`

## 2. 配置主题

在页面中新增主题并填写：

- 主题名称
- 查询提示词（prompt）
- 输出目录（folder）
- 可选：最少新闻条数（min_news_count）
- 可选：执行时间（hour/minute）

## 3. 立即执行查询

在页面点击“立即查询测试”，或调用接口：

```bash
curl -X POST http://localhost:8000/run_query \
  -H 'Content-Type: application/json' \
  -d '{"theme_id": 1774183263340}'
```

## 4. 时间解析（自然语言 -> cron）

页面“AI解析时间”按钮会调用后端 `/parse_time`。
也可手动请求：

```bash
curl -X POST http://localhost:8000/parse_time \
  -H 'Content-Type: application/json' \
  -d '{"description":"每天早上10点"}'
```

预期返回示例：

```json
{
  "success": true,
  "cron": "0 10 * * *",
  "hour": 10,
  "minute": 0,
  "display": "0 10 * * *"
}
```

## 5. 常用接口

- `GET /config.json` 读取配置
- `POST /save_config` 保存配置
- `GET /news_list` 查看新闻文件
- `POST /delete_news` 删除新闻
- `GET /logs` 查看操作日志
- `POST /add_log` 写入操作日志

## 6. 日志排查

- 服务日志：`logs/server-YYYY-MM-DD.log`
- 操作日志：`logs.json`

时间解析问题优先看服务日志中的：

- `[parse_time][trace_id] Agent请求输入`
- `[parse_time][trace_id] Agent responses状态`
- `[parse_time][trace_id] chat/completions回退状态`
- `[parse_time][trace_id] AI输出(原始)`

## 7. 依赖

- Go 1.24+
- `opencode` 命令（新闻查询执行依赖）
