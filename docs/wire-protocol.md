# 雷达回波线协议 v1

TCP 长连接，字节序统一为**小端序**。一个帧 = 固定头 + 负载 + CRC：

```
frame = header(80) + payload(range_bins) + crc32(4)
```

`crc32 = CRC32-IEEE(header || payload)`。

## 头字段（80 字节）

| 偏移 | 长度 | 类型 | 字段 | 说明 |
| ---:| ---:| --- | --- | --- |
| 0 | 4 | bytes | magic | 固定 ASCII `ICBR` |
| 4 | 2 | uint16 | version | 当前为 `1` |
| 6 | 2 | uint16 | flags | 保留，发 0 |
| 8 | 8 | uint64 | sweep_id | 切片唯一 ID（单调递增） |
| 16 | 8 | int64 | timestamp_ns | UTC Unix 纳秒 |
| 24 | 8 | float64 | ship_lat | 船位纬度（度） |
| 32 | 8 | float64 | ship_lon | 船位经度（度） |
| 40 | 8 | float64 | heading_deg | 船首向（相对真北，0..360） |
| 48 | 4 | float32 | azimuth_deg | 射线方位（相对船首向） |
| 52 | 4 | float32 | elev_deg | 天线仰角 |
| 56 | 4 | float32 | range_start | 首个距离 bin 量程（米） |
| 60 | 4 | float32 | range_bin | bin 间距（米） |
| 64 | 4 | uint32 | range_bins | 负载 bin 数（≤ 16384） |
| 68 | 4 | float32 | tx_gain_db | 发射增益 |
| 72 | 16 | bytes | reserved | 保留，全 0 |

## 负载

`range_bins` 个 `uint8`，表示各距离 bin 的回波强度：`0` 为最弱（开阔水面），
`255` 为最强。第 i 个 bin 的量程 = `range_start + i * range_bin`。

## 解析容错

1. 在缓冲区内查找 `ICBR`；
2. 用 `Peek` 校验版本与 `range_bins`；
3. 校验整帧 CRC；
4. 任一步失败则整体前进 1 字节继续扫描，绝不跨过坏帧内部的假 magic；
5. 通过校验后才从缓冲区提交该帧。
