package scheduler

import (
	"testing"
	"time"
)

func TestParseScheduleWithJitter(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantExpr    string
		wantJitter  time.Duration
		expectError bool
	}{
		{
			name:        "标准每天8点",
			input:       "0 8 *",
			wantExpr:    "0 8 *",
			wantJitter:  0,
			expectError: false,
		},
		{
			name:        "每天8点带30分钟浮动",
			input:       "0 8 * ~30m",
			wantExpr:    "0 8 *",
			wantJitter:  30 * time.Minute,
			expectError: false,
		},
		{
			name:        "周五周六48小时内随机",
			input:       "0 0 5 ~48h",
			wantExpr:    "0 0 5",
			wantJitter:  48 * time.Hour,
			expectError: false,
		},
		{
			name:        "2段式自动补齐每天",
			input:       "15 8",
			wantExpr:    "15 8 *",
			wantJitter:  0,
			expectError: false,
		},
		{
			name:        "旧格式 08:00 兼容升级",
			input:       "08:00",
			wantExpr:    "0 8 *",
			wantJitter:  30 * time.Minute,
			expectError: false,
		},
		{
			name:        "旧格式 60m 兼容升级",
			input:       "60m",
			wantExpr:    "0 8 *",
			wantJitter:  30 * time.Minute,
			expectError: false,
		},
		{
			name:        "拦截步长斜杠 /",
			input:       "0 */2 *",
			expectError: true,
		},
		{
			name:        "拦截分钟位通配符 * (高频暴击防范)",
			input:       "* 8 *",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, jitter, err := ParseScheduleWithJitter(tt.input)
			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error for %q, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.input, err)
			}
			if expr != tt.wantExpr {
				t.Errorf("expr = %q, want %q", expr, tt.wantExpr)
			}
			if jitter != tt.wantJitter {
				t.Errorf("jitter = %v, want %v", jitter, tt.wantJitter)
			}
		})
	}
}

func TestCheckCron(t *testing.T) {
	s := &Scheduler{}
	loc := time.Local

	// 1. 测试每日固定时间触发
	nowDay := time.Date(2026, 10, 7, 8, 30, 0, 0, loc) // 2026-10-07 08:30 (周三)
	lastTimeBefore := time.Date(2026, 10, 7, 7, 0, 0, 0, loc)
	lastTimeAfter := time.Date(2026, 10, 7, 8, 5, 0, 0, loc)

	// 上次抓取在今天 07:00，当前 08:30，配置 "0 8 *" -> 应触发
	should, _ := s.checkCron("0 8 *", lastTimeBefore, nowDay)
	if !should {
		t.Errorf("expected trigger for '0 8 *' when lastTime is before 08:00")
	}

	// 上次抓取在今天 08:05，当前 08:30，配置 "0 8 *" -> 不应重复触发
	should, _ = s.checkCron("0 8 *", lastTimeAfter, nowDay)
	if should {
		t.Errorf("did not expect duplicate trigger for '0 8 *' when already fetched at 08:05")
	}

	// 2. 测试仅工作日早报 (1-5)
	// 2026-10-12 是周一
	nowMonday := time.Date(2026, 10, 12, 9, 30, 0, 0, loc)
	lastSunday := time.Date(2026, 10, 11, 18, 0, 0, 0, loc)
	should, _ = s.checkCron("0 9 1-5", lastSunday, nowMonday)
	if !should {
		t.Errorf("expected weekday trigger on Monday")
	}

	// 2026-10-11 是周日
	nowSunday := time.Date(2026, 10, 11, 9, 30, 0, 0, loc)
	lastSaturday := time.Date(2026, 10, 10, 18, 0, 0, 0, loc)
	should, _ = s.checkCron("0 9 1-5", lastSaturday, nowSunday)
	if should {
		t.Errorf("did not expect weekday trigger on Sunday")
	}

	// 3. 测试周刊源 (周日 10 点，0 10 0)
	nowSunday1030 := time.Date(2026, 10, 11, 10, 30, 0, 0, loc)
	should, _ = s.checkCron("0 10 0", lastSaturday, nowSunday1030)
	if !should {
		t.Errorf("expected Sunday newsletter trigger")
	}
}
