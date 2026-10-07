package scheduler

import (
	"testing"
	"time"
)

func TestParseInterval(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
	}{
		{"30m", 30 * time.Minute},
		{"1h", 1 * time.Hour},
		{"2h", 2 * time.Hour},
		{"15", 15 * time.Minute},
		{"", 1 * time.Hour},
	}

	for _, tt := range tests {
		got := parseInterval(tt.input)
		if got != tt.expected {
			t.Errorf("parseInterval(%q) = %v; want %v", tt.input, got, tt.expected)
		}
	}
}

func TestCheckDailyFixed(t *testing.T) {
	s := &Scheduler{}
	loc := time.Local
	now := time.Date(2026, 10, 7, 8, 30, 0, 0, loc)

	// 上次抓取在今天 07:00，当前时间 08:30，目标配置为 "08:00" -> 应触发
	lastTime1 := time.Date(2026, 10, 7, 7, 0, 0, 0, loc)
	if !s.checkDailyFixed("08:00", lastTime1, now) {
		t.Errorf("expected trigger for 08:00, but did not")
	}

	// 上次抓取在今天 08:05，当前时间 08:30，目标配置为 "08:00" -> 不应重复触发
	lastTime2 := time.Date(2026, 10, 7, 8, 5, 0, 0, loc)
	if s.checkDailyFixed("08:00", lastTime2, now) {
		t.Errorf("did not expect trigger for 08:00 when lastTime is after target")
	}

	// 目标配置有多个时刻 "08:00,18:30"
	if !s.checkDailyFixed("08:00,18:30", lastTime1, now) {
		t.Errorf("expected trigger for multi-time target")
	}
}

