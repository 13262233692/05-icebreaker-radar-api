# 极地破冰船 · 冰情雷达回波解析后端

雷达前端通过 TCP 流推送二进制原始回波，服务完成**回波强度解析 → 海冰密集度计算 →
冰脊位置识别**，并通过 REST API 支持**时间范围 + 经纬度矩形**查询。

- **TCP 接入**：长连接接收自描述二进制帧，乱码/坏帧自动再同步，不丢流
- **回波解析**：8 位强度切片、CRC32(IEEE) 校验、协议版本校验
- **冰情分析**：密集度（冰覆盖 bin 占比）、均值/峰值强度、冰脊游程检测与地理定位
- **Redis**：ZSET 缓存最近 10 分钟回波切片（带分析结果），按分数滚动淘汰
- **PostgreSQL**：持久化逐切片冰情统计与冰脊记录，启动时自动建表
- **REST (Gin)**：回波 / 观测 / 冰脊 / 聚合摘要，统一时间窗 + bbox 过滤

## 模块划分

| 模块 | 目录 | 职责 |
| --- | --- | --- |
| `tcp_ingest` | `internal/tcp_ingest` | TCP 服务器、连接限流、解析→分析→缓存/落库流水线、批量写 PG、优雅关停 |
| `echo_parser` | `internal/echo_parser` | 二进制帧编解码、CRC32 校验、流式再同步读取器 |
| `ice_analyzer` | `internal/ice_analyzer` | 密集度计算、强度统计、冰脊游程识别、方位/距离→经纬度 |
| `http_api` | `internal/http_api` | Gin 路由、查询参数解析、bbox/时间过滤、聚合摘要、健康检查 |

辅助包：`internal/model`（线协议与领域模型、地理投影）、`internal/config`
（环境变量配置）、`internal/storage`（Redis / PostgreSQL 实现）。

可执行程序：

- `cmd/server`：单进程同时运行 TCP 接入与 HTTP API
- `cmd/radar_sim`：雷达前端模拟器，生成两块浮冰与三条冰脊的合成回波

## 快速开始

```bash
# 1) 启动 PostgreSQL + Redis
docker compose up -d postgres redis

# 2) 启动服务（TCP :9101，HTTP :8080）
go run ./cmd/server

# 3) 推送合成雷达数据
go run ./cmd/radar_sim -addr 127.0.0.1:9101

# 4) 查询
curl 'localhost:8080/api/v1/ice/summary'
curl 'localhost:8080/api/v1/ice/ridges?min_lat=78.20&max_lat=78.30&min_lon=15.10&max_lon=15.40'
curl 'localhost:8080/api/v1/echoes?limit=10&include_echo=1'
```

或整体容器化：`docker compose up -d --build`。

配置项见 `.env.example`，全部通过环境变量注入（`TCP_LISTEN_ADDR`、
`HTTP_LISTEN_ADDR`、`REDIS_ADDR`、`POSTGRES_DSN`、`ECHO_TTL`、
`ICE_THRESHOLD`、`RIDGE_THRESHOLD`、`RIDGE_MIN_BINS` 等）。

## 线协议

每个 TCP 帧为一条方位射线（一个回波切片），小端序：

```
+---------------- header (80B) ----------------+-- payload (N B) --+ crc32 (4B) +
| magic "ICBR" | ver | flags | sweep_id | ts_ns |  per-bin uint8    | IEEE       |
| ship lat/lon | heading | azimuth | elev ...   |  回波强度 0..255   |            |
+----------------------------------------------+-------------------+------------+
```

完整字段与偏移见 `internal/model/protocol.go` 与
[docs/wire-protocol.md](docs/wire-protocol.md)。CRC 覆盖 header+payload。
解析器遇到坏字节按字节滑动扫描下一个 magic，并在消费前完整校验版本与 CRC，
因此单帧损坏只会丢弃该帧，不会造成流错位。

## 分析算法

- **密集度**：强度 ≥ `ICE_THRESHOLD`(默认 100) 的距离 bin 数 / 有效 bin 数，值域 0..1。
- **冰脊**：沿射线查找强度 ≥ `RIDGE_THRESHOLD`(默认 170) 的最长连续游程，
  游程长度 ≥ `RIDGE_MIN_BINS`(默认 3) 判为一条压力冰脊；记录起止/峰值距离、
  峰值与均值强度。
- **定位**：距离 + 真方位（船首向 + 射线方位）在等距圆柱投影下换算为
  WGS-84 经纬度（雷达尺度数十公里内精度足够）；同时计算切片覆盖 bbox 用于矩形检索。

## REST API

基址 `/api/v1`。通用查询参数：

- `from` / `to`：RFC3339 或 Unix 秒，默认最近 1 小时
- `min_lat,max_lat,min_lon,max_lon`：经纬度矩形（四个需同时提供）
- `limit`：默认 500，上限 5000

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/health` | 服务及 PG/Redis 健康 |
| GET | `/api/v1/echoes` | Redis 最近 10 分钟切片；`include_echo=1` 返回原始强度 |
| GET | `/api/v1/ice/observations` | PG 逐切片密集度/强度统计 |
| GET | `/api/v1/ice/ridges` | PG 冰脊位置（峰点经纬度） |
| GET | `/api/v1/ice/summary` | 时间窗 + bbox 聚合：平均/最大/最新密集度、冰脊数 |
| GET | `/api/v1/ingest/stats` | 接入计数（解析帧、错误、丢弃、落库等） |

详见 [docs/api.md](docs/api.md)。

## 数据流

```
radar front-end ──TCP frames──▶ tcp_ingest ──parse/validate──▶ echo_parser
                                      │
                                      ▼
                                ice_analyzer (concentration + ridges + geo)
                                   │            │
                                   ▼            ▼
                         Redis ZSET (10 min)  PostgreSQL (batch, 1s / 64 rows)
                                   │            │
                                   └──────▶ http_api (Gin) ──▶ REST 查询
```

Redis 写失败不影响 PG 落库；流水线满时丢帧并累加
`dropped_backpressure` 计数（绝不反压阻塞雷达连接）。关停时先停止接收、
关闭连接，再排空内存中切片，确保已接收数据写入两个存储。

## 测试

```bash
go test -race ./...
```

- `echo_parser`：编解码往返、多帧流、垃圾再同步、CRC 损坏恢复、版本拒绝
- `ice_analyzer`：密集度、冰脊游程、覆盖 bbox、地理投影
- `tcp_ingest`：真实 TCP 回环端到端（缓存 + 批量落库计数）、坏字节恢复
- `http_api`：时间/bbox 过滤、非法参数 400、聚合摘要、健康检查

## 表结构（自动迁移）

- `ice_observations`：切片主键、时间、船位、方位、bin 数/最大量程、密集度、
  均值/冰均值/峰值强度、冰 bin 数、覆盖 bbox（时间与 bbox 索引）
- `ice_ridges`：所属切片外键、时间、起止/峰值 bin 与距离、宽度、峰/均值强度、
  峰点经纬度（时间与经纬度索引）
