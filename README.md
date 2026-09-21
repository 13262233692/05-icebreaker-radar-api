# 极地破冰船 · 冰情雷达回波解析后端 (Icebreaker Ice-Radar API)

船载冰情雷达前端通过 TCP 二进制流持续推送原始回波，本服务负责：

1. **TCP 接收与分帧**（`tcp_ingest`）
2. **二进制回波解析**（`echo_parser`）
3. **海冰密集度计算 + 压力冰脊识别**（`ice_analyzer`）
4. **REST 查询 API**（`http_api`，Gin）

中间件：**Redis** 缓存最近 10 分钟原始回波切片；**PostgreSQL** 持久化冰情统计与冰脊。

## 模块划分

| 模块 | 路径 | 职责 |
| --- | --- | --- |
| `tcp_ingest` | `internal/tcpingest` | TCP 监听、连接管理、流式帧读取，worker 池解析后写缓存/入库 |
| `echo_parser` | `internal/echoparser` | 自定义二进制协议编解码、魔数重同步、粘包/半包处理 |
| `ice_analyzer` | `internal/iceanalyzer` | 极坐标→经纬度投影、回波阈值分类、密集度统计、8-连通域 + PCA 冰脊识别 |
| `http_api` | `internal/httpapi` | Gin 路由，时间范围/经纬度矩形查询参数解析与校验 |
| `store` | `internal/store` | PostgreSQL（pgx 连接池）与 Redis（go-redis）存取层 |
| `config` | `internal/config` | 环境变量配置 |

## 二进制协议（大端序）

```
帧头 20 字节（定长）
  [0:4]   magic    "ICER" = 49 43 45 52
  [4]     version  = 1
  [5]     type     1=扫描帧 sweep   2=心跳 heartbeat
  [6:8]   header length = 20
  [8:12]  payload length
  [12:20] timestamp, unix 纳秒 (uint64)

sweep 载荷
  48 字节定长头：船位(lat/lon float64)、量程、射线数、每射线 bin 数、
  bin 间距、起始方位角、角步进、雷达频率、增益、flags
  随后 ray_count * bins_per_ray 个 uint8 回波强度样本（按射线排列）
```

解析器在魔数损坏时自动重同步，payload 超长（>64MiB）时丢弃并继续，
半包/粘包由带缓冲的流式 `Reader` 处理。

## 冰情分析

- **密集度**：强度 ≥ `ICE_THRESHOLD`（默认 120/255）的 bin 数 / 有效 bin 数。
- **冰脊识别**：对强度掩码做 8-连通域标记（射线按天线旋转方向环形连通），
  对每个连通域以米为单位做 2x2 协方差 PCA，要求：
  长轴 ≥ 60 m、长短轴比 ≥ 2、bin 数 ≥ 24，输出质心经纬度、长度、方位角。
- **投影**：以船为原点的等距矩形局部投影；在高纬度、数海里量程内精度足够，
  并使用 `111320*cos(lat)` 修正经度收缩。

## REST API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/health` | API/Postgres/Redis 健康检查 |
| GET | `/api/v1/status` | 连接数、收帧数、心跳时间、解析错误计数 |
| GET | `/api/v1/sweeps` | 冰情统计（支持时间范围 + 矩形区域） |
| GET | `/api/v1/ridges` | 冰脊位置（点在矩形内过滤） |
| GET | `/api/v1/concentration` | 密集度时间序列（`bucket_seconds` 分桶聚合） |
| GET | `/api/v1/echoes/recent` | 最近 10 分钟原始回波切片（Redis，base64 射线数据） |
| GET | `/api/v1/latest` | 最新 N 条统计 |

查询参数（除 `latest`/`echoes` 外通用）：

- `from`, `to`：RFC3339（`2026-09-21T00:00:00Z`）或 Unix 秒；缺省取最近 1 小时
- `min_lat`,`max_lat`,`min_lon`,`max_lon`：经纬度矩形，四个必须同时给出
- `limit`：返回条数上限（默认 1000，最大 5000）
- `bucket_seconds`：仅密集度序列，默认 60

示例：

```bash
curl "http://localhost:8080/api/v1/sweeps?min_lat=69.6&max_lat=69.8&min_lon=18.0&max_lon=19.0&from=2026-09-21T00:00:00Z"
curl "http://localhost:8080/api/v1/ridges?min_lat=69.6&max_lat=69.8&min_lon=18.0&max_lon=19.0"
curl "http://localhost:8080/api/v1/concentration?bucket_seconds=300"
```

## 数据存储

**PostgreSQL**

- `ice_sweeps`：每帧密集度、强度统计、船位、冰覆盖外接矩形（无冰时为 NULL）
- `ice_ridges`：冰脊质心、方位、长度、长宽比，外键级联删除
- 时间、船位、冰外接矩形均有索引；矩形查询用 AABB 重叠条件
  `min_lat <= max_lat_q AND max_lat >= min_lat_q ...`

**Redis**

- `ice:echo:sweep:<unix_nano>`：原始切片 JSON（射线数据 base64），TTL 10 分钟
- `ice:echo:index`：ZSET 时间索引，写入时顺带裁剪超窗成员
- `ice:echo:heartbeat`：最近一次雷达心跳

## 快速开始

### Docker Compose（推荐）

```bash
make up          # 启动 postgres + redis + api（同时构建镜像）
# 在另一个终端用模拟器压数据
docker compose run --rm api /usr/local/bin/iceradar-simulator -addr=api:9101
# 或本机直接跑模拟器（映射了 9101 端口）
go run ./cmd/simulator -addr=localhost:9101
```

### 本地运行

```bash
docker compose up -d postgres redis
make run         # 启动服务 :8080(HTTP) :9101(TCP)
make simulator   # 另一终端：模拟雷达前端
```

### 配置（环境变量，见 `.env.example`）

| 变量 | 默认 |
| --- | --- |
| `TCP_ADDR` | `:9101` |
| `HTTP_ADDR` | `:8080` |
| `POSTGRES_DSN` | `postgres://ice:ice@localhost:5432/iceradar?sslmode=disable` |
| `REDIS_ADDR` | `localhost:6379` |
| `CACHE_TTL_SECONDS` | `600` |
| `ICE_THRESHOLD` | `120` |
| `WORKERS` | `4` |

## 测试

```bash
make test        # 协议编解码/损坏重同步/半包、密集度与冰脊识别单测
```

`cmd/simulator` 生成含稳定压力冰脊、随时间漂移的冰场，可用于端到端演示。
