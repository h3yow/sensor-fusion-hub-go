# sensor-fusion-hub-go

一套**纯本地、可复用、确定性可回放**的多源传感器采集与融合组件。全部设备均为脚本化模拟设备，时间来自可控制的本地时钟，不依赖真实硬件、网络或外部服务。

## 1. 能力总览

- 多类型传感器源接入，统一**单位、时间戳、坐标系**；
- 多源并发采集，读取返回单一时钟下的**一致性快照**；
- 明确识别并按确定规则处理：乱序、丢包、迟到、源重启（epoch 推进）、源故障；
- 校准参数带**版本（calibration epoch）**，更新后新旧数据不串算；
- 源离线 / 质量下降时给出**可区分的状态与原因码**；
- 同一批输入回放结果逐字节级可复现（无随机数、无 sleep 驱动的判定逻辑）。

## 2. 支持的数据源类型

| Kind | 支持单位 | 支持坐标系 | 归一化后（参与融合的形式） |
| --- | --- | --- | --- |
| `temperature` | `C`、`F`、`K` | `thermal` | 摄氏度，F/K 自动换算 |
| `imu` | `rad/s`（rad/s + m/s²）、`deg/s`（deg/s + g） | `body`、`enu`、`ned` | 陀螺 rad/s、加速度 m/s²（1 g = 9.80665），坐标系保持声明值 |
| `gps` | `lat,lon,alt`、`ecef` | `wgs84`、`ecef` | LLA/WGS84 或 ECEF 米，二选一 |

每个源由 `source.Descriptor{ID, Kind, Unit, Frame, Address, Period}` 描述。

### 模拟设备（`pkg/source`）

`SimSource` 是确定性的脚本化设备：

- `ScriptedSample` 可声明 `Epoch / Seq / SampleTS / Quality`，以及 `GapAfter`（模拟丢一个包）、`FailAfter`（发完即故障）、`RestartAfter`（发完重启并推进 epoch）；
- `Inject()` 用于逐样本回放驱动；`Fail()` / `Restart()` 模拟设备掉电与重启；
- 故障期间 `Inject` 直接返回 `ErrDeviceFailed`，不会静默缓存。

## 3. 接入与兼容规则（冲突一律拒绝）

`hub.New` / `hub.AddSource` 执行整单校验，任一条不满足则**整单拒绝**，不会半注册、不会静默覆盖：

1. **重复设备 ID** → `sensor.ErrDuplicateSource`；
2. **不支持的采样单位 / 坐标系**（如温度给 `Rankine`）→ `ErrUnsupportedUnit` / `ErrUnsupportedFrame`；
3. **单位与坐标系自相矛盾**（如 GPS 声明 `lat,lon,alt` 却配 `ecef` 帧）→ `ErrConfigConflict`；
4. **地址冲突**（两个 ID 占用同一 `Address`）→ `ErrConfigConflict`；
5. **同类传感器的归一化结果必须一致**：温度只能融合同为摄氏的源；GPS 的 LLA/WGS84 与 ECEF 两种变体**禁止混用**，同帧要求同样适用于 IMU（body 与 enu/ned 不互转、不混算）。

数据侧：未注册源的数据 → `ErrUnknownSource`。

## 4. 采集与判定规则（确定性）

判定仅依赖 `(Seq, Epoch, SampleTS, RecvTS, Quality)` 与时钟读数，规则固定：

| 现象 | 规则 | 状态/原因 |
| --- | --- | --- |
| 乱序 | 同一 epoch 内 `Seq` 回退或 `SampleTS` 倒退 → 拒绝，不替换最新值 | `reason=out_of_order`，`ErrOutOfOrder` |
| 丢包 | `Seq` 跳号 → 计数 `GapCount`，后续正常样本仍接收 | 状态维持 online |
| 迟到 | `RecvTS - SampleTS > MaxLate`（默认 2s）→ 拒绝，永不当最新 | `reason=late` |
| 源重启 | `Epoch` 增大 → 旧时间线作废、seq 重新计数合法、故障标记清除；旧 epoch 再来数据拒绝 | `restarted / epoch_bump`，旧 epoch 数据 `ErrStale` |
| 新鲜度 | `now - LastRecv` 超过 `FreshnessWindow`（温度/GPS 2s、IMU 1s）→ stale；再超 `OfflineAfter`（默认 5s）→ offline | `stale/offline + no_fresh_data` |
| 质量低 | `Quality < QualityFloor`（默认 0.3）→ 标记且不参与融合 | `degraded / quality_low` |
| 源故障 | 错误通道或 `MarkSourceFailed` → 立即摘出融合，直到更大 epoch 的样本到达 | `offline / source_error` |

**故障隔离**：每个源由独立 goroutine 采集，单个源 panic/err 只记录该源状态，其他源继续采集与融合。

## 5. 融合与降级语义

每类传感器产生一个 `FusedResult`，采用**质量×新鲜度线性衰减权重**的加权平均：

- 只有 `now` 时刻权重为正（在线、新鲜、质量达标、非重启未证实）的源参与；
- 贡献者按 SourceID 排序，保证输出可复现；
- 校准 epoch 不一致的样本被排除（见下节）；
- 结果状态分级清晰可区分：
  - `online`：所有可用源都参与；
  - `degraded / single_source`：仅剩 1 个源（冗余丢失）；
  - `degraded / partial_sources`：部分源因 stale/质量被排除；
  - `stale / no_fresh_data`：没有任何可融合源，结果不携带数值结论。

## 6. 校准版本语义（新旧不串算）

- `Registry` 中每个源的参数历史**只追加**：epoch 0 为单位参数，每次 `UpdateCalibration` 单调产生新 epoch；
- 样本入库时打上**当时的 epoch 标签**；融合只接受每个源当前 epoch 的样本；
- 参数更新瞬间，该源**最新的一条**观测会用新参数重算并提升到新 epoch，更老的观测不保留——因此融合窗口内不可能混入旧参数结果；
- 参数本身做校验（有限数、温度只能用分量 0、GPS scale 必须为 1 等），非法更新被拒绝，epoch 不前进。

## 7. 日志（输入时间线 / 判定依据 / 最终状态）

每次入库输出一条 `event=input_timeline` 结构化日志，包含：

```
id, seq, epoch, cal_epoch, sample_ts, recv_ts, quality,
accepted, verdict_status, verdict_reason
```

此外记录 `fusion: result`（kind/status/reason/contributors/cal_epoch）、
`hub: source failure`、`hub: calibration updated` 等。测试中的 `captureLogger`
会对这些字段做断言。

## 8. 本地验证方法

要求 Go 1.25+，无需任何外部依赖：

```bash
# 编译
go build ./...

# 全部自动化测试（含竞态检测、重复运行验证可复现性）
go test ./... -race -count=3

# 确定性演示：丢包 / 低质量 / 故障+重启 / 校准更新
go run ./cmd/sfdemo
```

`go run ./cmd/sfdemo` 每次输出相同：可以看到 t+3 的丢包与低质量降级、
t+5 的 `restarted/epoch_bump`、校准后温度融合值随新 epoch 变化等完整过程。

### 测试覆盖对应关系

| 需求点 | 测试 |
| --- | --- |
| 设备接入：多单位共存、重复设备、非法单位、配置冲突、地址冲突 | `pkg/hub/registration_test.go` |
| 乱序 / 丢包 / 迟到 / stale→offline | `pkg/hub/timeline_test.go` |
| 源重启：旧 epoch 拒绝、新 seq 合法、状态恢复 | `TestSourceRestartOldEpochRejected`、`pkg/engine` |
| 源故障不拖垮其他源 | `TestFailingSourceDoesNotAffectOthers` |
| 并发采集 + 并发一致性快照 | `TestConcurrentSnapshotsAreConsistent`（-race） |
| 校准更新后新旧不串算 / 非法参数拒绝 | `TestCalibrationUpdateIsolatesOldAndNew`、`pkg/calibrate` |
| 同批输入可复现 | `TestReplayDeterminism`、`TestEndToEndScriptedReplay` |

## 9. 包结构

```
pkg/sensor      核心数据模型、单位/坐标归一化、错误码、Logger 接口
pkg/clock       WallClock 与并发安全的 ScriptedClock（确定性时间）
pkg/source      Source 接口与脚本化模拟设备
pkg/calibrate   带版本的校准参数注册表与纯函数 Apply
pkg/engine      每源状态机（时序/健康判定）与加权融合
pkg/hub         配置校验、装配、采集 goroutine、一致性快照（对外门面）
pkg/logging     slog 实现
cmd/sfdemo      本地确定性演示
```
