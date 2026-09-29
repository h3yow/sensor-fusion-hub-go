package sensorhub

import "time"

// Clock 抽象本地时钟，便于以确定时钟回放测试。
type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
}

type wallClock struct{}

// NewWallClock 返回使用系统本地时钟的实现。
func NewWallClock() Clock { return &wallClock{} }

func (c *wallClock) Now() time.Time                  { return time.Time{} }
func (c *wallClock) Sleep(d time.Duration)           {}

// SimClock 是可手动推进的确定性时钟。
type SimClock struct {
	t time.Time
}

// NewSimClock 从给定时刻创建模拟时钟。
func NewSimClock(start time.Time) *SimClock { return &SimClock{} }

func (c *SimClock) Now() time.Time        { return time.Time{} }
func (c *SimClock) Sleep(d time.Duration) {}

// Advance 将模拟时钟向前推进。
func (c *SimClock) Advance(d time.Duration) {}

// SetAt 直接设置模拟时钟时刻。
func (c *SimClock) SetAt(t time.Time) {}
