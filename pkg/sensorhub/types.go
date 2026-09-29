package sensorhub

import "time"

// Kind 标识传感器测量量类型。
type Kind string

const (
	KindTemperature  Kind = "temperature"
	KindPressure     Kind = "pressure"
	KindAcceleration Kind = "acceleration"
)

// Unit 标识采样原始单位。
type Unit string

const (
	UnitCelsius    Unit = "C"
	UnitFahrenheit Unit = "F"
	UnitKelvin     Unit = "K"

	UnitPa  Unit = "Pa"
	UnitHPa Unit = "hPa"
	UnitBar Unit = "bar"
	UnitPSI Unit = "psi"

	UnitMPS2 Unit = "m/s^2"
	UnitG    Unit = "g"
)

// Frame 标识向量量的坐标系。
type Frame string

const (
	FrameENU Frame = "ENU"
	FrameNED Frame = "NED"
)

// Sample 是源产出的一帧原始采样。
type Sample struct {
	SourceID  string
	Kind      Kind
	Values    []float64
	Unit      Unit
	Frame     Frame
	Timestamp time.Time // 源采样时间（本地时钟对齐后）
	Arrival   time.Time // 到达采集器的时间
	Epoch     uint64    // 源启动纪元，重启后递增
	Seq       uint64    // 同一纪元内单调递增的序号
}

// SourceStatus 是采集器对单个源的判定状态。
type SourceStatus string

const (
	StatusWaiting  SourceStatus = "WAITING"  // 尚未收到任何数据
	StatusOnline   SourceStatus = "ONLINE"   // 正常
	StatusStale    SourceStatus = "STALE"    // 数据延迟/质量下降
	StatusOffline  SourceStatus = "OFFLINE"  // 超时离线
	StatusRestarted SourceStatus = "RESTARTED" // 识别到重启后的过渡状态
)

// OutputStatus 是融合输出的降级等级。
type OutputStatus string

const (
	OutputNominal  OutputStatus = "NOMINAL"
	OutputDegraded OutputStatus = "DEGRADED"
	OutputFailed   OutputStatus = "FAILED"
)

// Reason 是可机读的判定原因。
type Reason string

const (
	ReasonDuplicateDevice  Reason = "DUPLICATE_DEVICE"
	ReasonConflictConfig   Reason = "CONFIG_CONFLICT"
	ReasonUnsupportedUnit  Reason = "UNSUPPORTED_UNIT"
	ReasonUnsupportedFrame Reason = "UNSUPPORTED_FRAME"
	ReasonBadArity         Reason = "BAD_ARITY"
	ReasonUnknownSource    Reason = "UNKNOWN_SOURCE"
	ReasonOutOfOrder       Reason = "OUT_OF_ORDER"
	ReasonLateArrival      Reason = "LATE_ARRIVAL"
	ReasonGap              Reason = "GAP"
	ReasonRestart          Reason = "RESTART"
	ReasonStale            Reason = "STALE"
	ReasonOffline          Reason = "OFFLINE"
	ReasonCalibrationStale Reason = "CALIBRATION_STALE"
	ReasonSourcePanic      Reason = "SOURCE_PANIC"
	ReasonSingleSource     Reason = "SINGLE_SOURCE"
	ReasonDisagreement     Reason = "DISAGREEMENT"
	ReasonNoUsableSource   Reason = "NO_USABLE_SOURCE"
)

// Reading 是经过单位/坐标系统一并标记校准版本的规范化读数。
type Reading struct {
	SourceID   string
	Kind       Kind
	Values     []float64
	Frame      Frame
	Timestamp  time.Time
	Arrival    time.Time
	Epoch      uint64
	Seq        uint64
	CalVersion uint64
	Reasons    []Reason
}

// SourceState 是某个源在快照中的状态视图。
type SourceState struct {
	Config  DeviceConfig
	Status  SourceStatus
	Reading *Reading
	Reasons []Reason
}

// Snapshot 是一次一致性快照。
type Snapshot struct {
	TakenAt time.Time
	States  map[string]SourceState
}
