package sensorhub

import "time"

// Source 是一个可采集的传感器源（含模拟设备实现）。
type Source interface {
	ID() string
	Config() DeviceConfig
	// Emit 主动产出一帧；ok=false 表示当前无数据。
	Emit(now time.Time) (Sample, bool)
}

// ScriptedSource 按预置脚本回放采样的模拟设备。
type ScriptedSource struct {
	Script []ScriptedSample
}

// ScriptedSample 描述脚本中的一帧（相对起始时刻的偏移）。
type ScriptedSample struct {
	Offset time.Duration
	Sample Sample
}

func (s *ScriptedSource) ID() string                       { return "" }
func (s *ScriptedSource) Config() DeviceConfig             { return DeviceConfig{} }
func (s *ScriptedSource) Emit(now time.Time) (Sample, bool) { return Sample{}, false }
