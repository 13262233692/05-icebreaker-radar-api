# 冰情雷达回波解析后端（Icebreaker Radar API）

面向极地破冰船的冰情雷达后端服务：雷达前端通过 TCP 流推送二进制原始回波，
服务实时解析回波强度、计算海冰密集度、识别冰脊位置，并通过 REST API 提供时空范围查询。

## 架构

```
雷达前端 ──TCP(二进制帧)──> tcp_ingest ──> echo_parser ──> ice_analyzer ──┬─> Redis (最近10分钟滑动窗口)
                                                                        └─> PostgreSQL (冰情统计持久化)
REST 查询 <── Gin (http_api) <── Redis / PostgreSQL
```

## 模块划分

| 模块 | 职责 |
|---|---|
| `internal/tcp_ingest` | TCP 监听、连接管理、按帧拆包、分发到处理器 |
| `internal/echo_parser` | 二进制帧协议定义与编解码（30 字节头 + N×2 字节采样） |
| `internal/ice_analyzer` | 海冰密集度计算、冰脊峰值识别（阈值 + 突出度 + 去抖） |
| `internal/http_api` | Gin REST API：最近窗口查询、时空范围统计查询 |
| `internal/cache` | Redis ZSET 滑动窗口（最近 10 分钟回波切片） |
| `internal/store` | PostgreSQL 持久化与时间范围 + 经纬度矩形查询 |
| `cmd/server` | 服务装配入口 |
| `cmd/simulator` | 雷达前端模拟器（推送合成回波，用于联调） |

## 二进制帧协议（大端序）

| 字段 | 长度 | 说明 |
|---|---|---|
| Magic | 2B | 固定 `0x4943` ("IC") |
| Version | 1B | 当前为 `1` |
| MsgType | 1B | `0x01` = 回波帧 |
| Timestamp | 8B | Unix 纳秒 |
| Latitude / Longitude | 8B + 8B | float64 |
| SampleCount | 2B | 采样点数（≤ 4096） |
| Samples | N×2B | uint16 回波强度（0–65535） |

## 快速开始

```bash
docker compose up -d                 # 启动 PostgreSQL + Redis
cp .env.example .env                 # 可选，默认配置即可
go run ./cmd/server                  # 启动服务（TCP :9000, HTTP :8080）
go run ./cmd/simulator               # 另开终端，推送模拟回波
```

## REST API

- `GET /healthz` — 健康检查
- `GET /api/v1/ice/recent` — 最近 10 分钟回波分析切片（Redis）
  - 可选：`min_lat, max_lat, min_lon, max_lon`（窗口内矩形过滤）
- `GET /api/v1/ice/stats` — 冰情统计时空查询（PostgreSQL）
  - 必填：`start, end`（RFC3339 或 Unix 秒）
  - 可选：`min_lat, max_lat, min_lon, max_lon, limit`

示例：

```bash
curl 'http://localhost:8080/api/v1/ice/recent?min_lat=78.2&max_lat=78.3&min_lon=15.6&max_lon=15.7'

curl 'http://localhost:8080/api/v1/ice/stats?start=2026-09-22T00:00:00Z&end=2026-09-23T00:00:00Z&min_lat=78&max_lat=79&min_lon=15&max_lon=16&limit=100'
```

## 分析算法

- **海冰密集度**：回波强度 ≥ `IceThreshold` 的采样点占比（0.0–1.0）。
- **冰脊识别**：局部极大值且峰值 ≥ `RidgeThreshold`，相对两侧谷值突出度
  ≥ `RidgeMinProminence`，并按 `RidgeMinDistance` 最小间距去抖；
  位置换算为距雷达的水平距离（`MetersPerSample` × 采样下标）。

阈值可在 `cmd/server/main.go` 中通过 `ice_analyzer.DefaultConfig()` 调整。

## 测试

```bash
go test ./...
```
