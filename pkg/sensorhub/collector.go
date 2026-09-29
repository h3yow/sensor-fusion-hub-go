package sensorhub

import "time"

// Collector 负责并发采集、规范化与每源状态判定。
type Collector struct{}

func NewCollector(reg *Registry, cals *CalibrationStore, log *EventLog, clock Clock) *Collector {
	return &Collector{}
}

// Ingest 接收一帧原始采样（并发安全）。
func (c *Collector) Ingest(s Sample) error { return nil }

// Snapshot 返回一次一致性快照。
func (c *Collector) Snapshot() Snapshot { return Snapshot{} }

// Reap 根据当前时钟将超时源标记为 STALE/OFFLINE。
func (c *Collector) Reap(now time.Time) {}
