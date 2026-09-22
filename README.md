# 极地破冰船冰情雷达回波解析后端

接收雷达前端经 TCP 推送的二进制回波流，解析回波强度、计算海冰密集度、
识别冰脊位置，并通过 REST API 支持按**时间范围 + 经纬度矩形区域**查询冰情。

- **TCP 接入与二进制解析**：Go 原生 TCP 服务，长度无关的魔数分帧 + CRC32 校验，支持失步自动重同步。
- **冰情分析**：阈值法计算海冰密集度（0–1），连续高后向散射 bin 段识别冰脊，并按船位/艏向/方位/距离做 WGS-84 地理定位。
- **Redis**：ZSET（时间索引）+ 单帧键（独立 TTL）缓存最近 10 分钟完整回波切片，供实时查询。
- **PostgreSQL**：批量事务持久化每扫描周期统计与冰脊记录，支持时间/经纬度矩形查询与汇总。
- **Gin REST API**：实时回波、历史统计、冰脊位置、区域汇总等接口。

## 模块划分

| 包 | 职责 |
| --- | --- |
| `internal/tcp_ingest` | TCP 监听、连接管理、读取二进制帧并送入管道 |
| `internal/echo_parser` | 二进制协议编解码、魔数同步、CRC32 校验、字段校验 |
| `internal/ice_analyzer` | 回波强度统计、海冰密集度、冰脊检测与地理定位 |
| `internal/http_api` | Gin 路由、时间范围/经纬度矩形查询参数、响应处理 |
| `internal/cache` | Redis 近期回波切片缓存 |
| `internal/storage` | PostgreSQL 迁移、批量写入、历史查询 |
| `internal/pipeline` | 串联 解析 → 分析 → 缓存 → 批量落库，背压与计数 |
| `cmd/server` | 服务入口（优雅关闭） |
| `cmd/radar_sim` | 雷达前端模拟器，用于本地/联调测试 |

## 二进制协议

小端序，单帧结构（详见 `internal/echo_parser/protocol.go` 头注释）：

```
魔数 "IR"(2) | 版本(1) | 保留(1) | frame_id u32 | sweep_id u32 |
时间戳 ns i64 | 船纬度 f64 | 船经度 f64 | 艏向 f64 | 距离分辨率 f32 |
起始方位 f32 | 结束方位 f32 | range_bins u16 | 保留 u32 |
强度[range_bins] u8 | CRC32(头+载荷) u32
```

接收端遇到坏帧（CRC/魔数错误）会自动扫描下一个魔数重新同步，不会断连。

## REST API

所有查询公共参数（均可选）：

- `start` / `end`：RFC3339 时间，缺省取最近窗口（回波默认 10 分钟，历史默认 1 小时）
- `min_lat` / `max_lat` / `min_lon` / `max_lon`：经纬度矩形，四个参数必须同时给出
- `limit`：返回条数，默认 1000，最大 10000

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/health` | 存活检查、Redis 状态、接入计数 |
| GET | `/api/v1/echoes/latest` | 最新一帧完整回波切片（Redis） |
| GET | `/api/v1/echoes` | 最近窗口内完整回波切片（Redis，含 bbox 过滤） |
| GET | `/api/v1/stats` | 历史每扫描周期冰情统计（PostgreSQL） |
| GET | `/api/v1/stats/summary` | 时间/区域窗口聚合（平均/最大密集度、冰脊总数） |
| GET | `/api/v1/ridges` | 冰脊检测记录（方位、距离、经纬度、峰值强度） |

`echoes` 中 `intensity` 字段为 `[]byte` 的 JSON base64 编码。

示例：

```bash
curl 'http://localhost:8080/api/v1/stats?start=2026-09-22T00:00:00Z&end=2026-09-23T00:00:00Z&min_lat=78.0&max_lat=79.0&min_lon=15.0&max_lon=16.0'
curl 'http://localhost:8080/api/v1/ridges?min_lat=78.1&max_lat=78.3&min_lon=15.4&max_lon=15.8'
curl 'http://localhost:8080/api/v1/stats/summary'
```

## 运行

### Docker Compose（PostgreSQL + Redis + 服务）

```bash
docker compose up -d --build
```

### 本地运行

```bash
make infra          # 启动 PostgreSQL / Redis 容器
make run            # 启动服务（:8080 HTTP, :9101 TCP）
make sim            # 另开终端：启动雷达模拟器推流
```

服务启动时自动执行嵌入的 SQL 迁移，无需手工建表。

## 配置（环境变量）

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `TCP_ADDR` | `:9101` | 雷达回波 TCP 监听地址 |
| `HTTP_ADDR` | `:8080` | REST API 监听地址 |
| `REDIS_URL` | `redis://localhost:6379/0` | Redis 连接 |
| `POSTGRES_DSN` | `postgres://radar:radar@localhost:5432/iceradar?sslmode=disable` | PG DSN |
| `ECHO_TTL_SECONDS` | `600` | 回波切片缓存时长（最近 10 分钟） |
| `ICE_THRESHOLD` | `120` | 海冰判定强度阈值 |
| `RIDGE_THRESHOLD` | `200` | 冰脊判定强度阈值 |
| `RIDGE_MIN_BINS` / `RIDGE_MIN_RUN` | `4` / `2` | 冰脊最小强 bin 数 / 最小连续长度 |
| `BATCH_SIZE` / `BATCH_FLUSH_MS` | `100` / `1000` | 落库批量大小与定时刷盘 |

## 算法说明

- **海冰密集度** = 强度 ≥ `ICE_THRESHOLD` 的距离库数 / 总距离库数。
- **冰脊**：在一个扫描扇区内查找强度 ≥ `RIDGE_THRESHOLD` 的连续段，
  长度达到最小连续 bin 数即判为冰脊；记录起止/峰值 bin、平均/峰值强度，
  并结合船位、艏向与扇区方位（支持跨 0/360° 扇区）用大圆目标点公式计算经纬度。

## 测试

```bash
make test
```
