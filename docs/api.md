# REST API

## 通用

- 基址：`/api/v1`
- 时间：`from` / `to`，接受 RFC3339（`2026-09-21T08:00:00Z`）或 Unix 秒；
  默认窗口为最近 1 小时，且要求 `from < to`
- 矩形：`min_lat,max_lat,min_lon,max_lon` 必须**同时**提供，
  `-90 ≤ min_lat ≤ max_lat ≤ 90`，`-180 ≤ min_lon ≤ max_lon ≤ 180`
- `limit`：正整数，默认 500，上限 5000
- 非法参数返回 `400 {"error": "..."}`

## GET /health

返回服务与依赖状态；任一依赖不可用时 HTTP 503、`status=degraded`。

```json
{"status":"ok","dependencies":{"postgres":"ok","redis":"ok"}}
```

## GET /api/v1/echoes

来自 Redis 的最近 10 分钟切片（含分析结果）。请求时间窗会被收敛到缓存窗口。
加 `include_echo=1` 才返回原始强度数组（base64 的 JSON byte 数组）。

```bash
curl 'localhost:8080/api/v1/echoes?limit=20&include_echo=1'
```

## GET /api/v1/ice/observations

PostgreSQL 中逐切片统计：密集度、均值/冰均值/峰值强度、冰 bin 数、覆盖 bbox。

```bash
curl 'localhost:8080/api/v1/ice/observations?from=2026-09-21T00:00:00Z&to=2026-09-21T12:00:00Z&min_lat=78&max_lat=79&min_lon=14&max_lon=16'
```

矩形命中规则：切片覆盖盒与查询矩形在两轴上区间相交。

## GET /api/v1/ice/ridges

冰脊检测记录，按峰点经纬度进行矩形包含判断，按时间倒序返回。

## GET /api/v1/ice/summary

在时间窗 + 矩形内聚合：

```json
{"summary":{"observations":461,"ridge_count":22,
"mean_concentration":0.091,"max_concentration":0.586,
"latest_concentration":0.12,"latest_time":"2026-09-21T00:59:03Z",
"latest_ship_lat":78.2297,"latest_ship_lon":15.1203}}
```

## GET /api/v1/ingest/stats

TCP 接入计数：接收/活跃连接、解析帧、解析错误、Redis 写、落库行数、
落库错误、背压丢弃。
