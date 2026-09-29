package sensorhub

import "time"

// Hub 组合注册、采集、校准与融合，提供本地可运行入口。
type Hub struct{}

func NewHub(clock Clock) *Hub { return &Hub{} }

func (h *Hub) Registry() *Registry         { return nil }
func (h *Hub) Calibrations() *CalibrationStore { return nil }
func (h *Hub) Collector() *Collector       { return nil }
func (h *Hub) Fuser() *Fuser               { return nil }
func (h *Hub) EventLog() *EventLog         { return nil }

// RunSource 在独立 goroutine 中运行一个源，panic 被隔离不影响其他源。
func (h *Hub) RunSource(src Source, interval time.Duration) {}

// Stop 停止所有由 Hub 启动的采集 goroutine。
func (h *Hub) Stop() {}
