package scheduler

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"reader/internal/db"
	"reader/internal/fetcher"
)

var threeFieldCronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dow | cron.Descriptor,
)

type Scheduler struct {
	db           *db.DB
	fetcher      *fetcher.Fetcher
	fetchTimeout time.Duration
	stopChan     chan struct{}
	mu           sync.Mutex
	isBusy       map[int64]bool
	lastFired    map[int64]time.Time
}

func NewScheduler(database *db.DB, f *fetcher.Fetcher, timeoutSeconds int) *Scheduler {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 45
	}
	return &Scheduler{
		db:           database,
		fetcher:      f,
		fetchTimeout: time.Duration(timeoutSeconds+15) * time.Second,
		stopChan:     make(chan struct{}),
		isBusy:       make(map[int64]bool),
		lastFired:    make(map[int64]time.Time),
	}
}

// Start 启动调度主循环 (每 30 秒检查一次需要拉取的订阅)
func (s *Scheduler) Start() {
	ticker := time.NewTicker(30 * time.Second)
	go func() {
		log.Println("[Scheduler] Started background feed scheduler")
		// 启动时先检查一次
		s.checkAndFetchAll()

		for {
			select {
			case <-ticker.C:
				s.checkAndFetchAll()
			case <-s.stopChan:
				ticker.Stop()
				log.Println("[Scheduler] Stopped background feed scheduler")
				return
			}
		}
	}()
}

func (s *Scheduler) Stop() {
	close(s.stopChan)
}

// checkAndFetchAll 检查每个订阅是否到了拉取时间
func (s *Scheduler) checkAndFetchAll() {
	feeds, err := s.db.GetAllFeeds()
	if err != nil {
		log.Printf("[Scheduler] Error fetching feeds list: %v\n", err)
		return
	}

	now := time.Now()
	for _, feed := range feeds {
		if should, maxJitter := s.shouldFetch(feed, now); should {
			s.mu.Lock()
			if s.isBusy[feed.ID] {
				s.mu.Unlock()
				continue
			}
			s.isBusy[feed.ID] = true
			s.mu.Unlock()

			fID := feed.ID
			fTitle := feed.Title
			go func(feedJitter time.Duration) {
				var jitter time.Duration
				if feedJitter > 0 {
					// 在 [0, feedJitter] 范围内生成随机延迟
					jitter = time.Duration(rand.Int64N(int64(feedJitter)))
				} else {
					// 默认 10 ~ 30 秒的保底随机削峰 (Random Jitter)
					jitterSeconds := 10 + rand.IntN(21)
					jitter = time.Duration(jitterSeconds) * time.Second
				}
				log.Printf("[Scheduler] Feed [%d] %s matched trigger, applying %v random jitter...\n", fID, fTitle, jitter)

				select {
				case <-time.After(jitter):
				case <-s.stopChan:
					s.mu.Lock()
					delete(s.isBusy, fID)
					s.mu.Unlock()
					return
				}

				s.doFetch(fID)
			}(maxJitter)
		}
	}
}

// FetchNow 立即手动触发单次抓取 (无需等待随机延迟)
func (s *Scheduler) FetchNow(feedID int64) (int, error) {
	s.mu.Lock()
	if s.isBusy[feedID] {
		s.mu.Unlock()
		return 0, fmt.Errorf("feed is already being fetched")
	}
	s.isBusy[feedID] = true
	s.mu.Unlock()

	return s.doFetch(feedID)
}

func (s *Scheduler) doFetch(feedID int64) (int, error) {
	defer func() {
		s.mu.Lock()
		delete(s.isBusy, feedID)
		s.lastFired[feedID] = time.Now()
		s.mu.Unlock()
	}()

	feed, err := s.db.GetFeedByID(feedID)
	if err != nil {
		return 0, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.fetchTimeout)
	defer cancel()

	log.Printf("[Fetcher] Fetching feed [%d] %s (%s)...\n", feed.ID, feed.Title, feed.FeedURL)
	count, err := s.fetcher.FetchFeed(ctx, feed)
	if err != nil {
		log.Printf("[Fetcher] Fetching feed [%d] failed: %v\n", feed.ID, err)
		return 0, err
	}
	log.Printf("[Fetcher] Feed [%d] fetch completed, added %d new articles\n", feed.ID, count)
	return count, nil
}

// shouldFetch 核心判定函数：支持 3 段式 Cron 调度，跳过 paused (暂停)
func (s *Scheduler) shouldFetch(feed *db.Feed, now time.Time) (bool, time.Duration) {
	// 如果订阅处于暂停状态，永不自动调度抓取
	if feed.ScheduleType == "paused" {
		return false, 0
	}

	s.mu.Lock()
	busy := s.isBusy[feed.ID]
	lastFired := s.lastFired[feed.ID]
	s.mu.Unlock()

	if busy {
		return false, 0
	}

	lastTime := feed.CreatedAt
	if feed.LastFetchedAt != nil {
		lastTime = *feed.LastFetchedAt
	}
	if lastFired.After(lastTime) {
		lastTime = lastFired
	}
	if lastTime.IsZero() {
		lastTime = now
	}

	return s.checkCron(feed.ScheduleValue, lastTime, now)
}

// checkCron 检查 3 段式 Cron 是否到达触发时间
func (s *Scheduler) checkCron(val string, lastTime, now time.Time) (bool, time.Duration) {
	cronExpr, maxJitter, err := ParseScheduleWithJitter(val)
	if err != nil {
		// 校验未通过时使用安全默认值: 每天 8 点，浮动 30 分钟
		cronExpr = "0 8 *"
		maxJitter = 30 * time.Minute
	}

	sched, err := threeFieldCronParser.Parse(cronExpr)
	if err != nil {
		return false, 0
	}

	// 只要当前时间 >= 上次抓取后的下一次计划时刻，即触发抓取
	nextTime := sched.Next(lastTime)
	if !now.Before(nextTime) {
		return true, maxJitter
	}
	return false, 0
}

// ParseScheduleWithJitter 解析并校验 3 段式 Cron 与可选浮动窗口
// 格式: "分 时 周 [~浮动时长]"，例如 "0 8 * ~30m", "0 10 0", "0 8,18 *"
func ParseScheduleWithJitter(val string) (cronExpr string, maxJitter time.Duration, err error) {
	val = strings.TrimSpace(val)
	if val == "" {
		val = "0 8 * ~30m"
	}

	// 兼容老数据格式：如果含有冒号 "08:00"，或纯时长无空格的 "60m"/"1h"
	if strings.Contains(val, ":") || (!strings.Contains(val, " ") && (strings.HasSuffix(val, "m") || strings.HasSuffix(val, "h"))) {
		val = "0 8 * ~30m"
	}

	parts := strings.Split(val, "~")
	cronExpr = strings.TrimSpace(parts[0])

	if len(parts) > 1 {
		jitterStr := strings.TrimSpace(parts[1])
		if d, parseErr := time.ParseDuration(jitterStr); parseErr == nil && d > 0 {
			maxJitter = d
		}
	}

	// 禁用斜杠 / (步长/伪间隔)
	if strings.Contains(cronExpr, "/") {
		return "", 0, fmt.Errorf("日历时刻模式不支持 '/' 步长语法")
	}

	fields := strings.Fields(cronExpr)
	if len(fields) < 2 || len(fields) > 3 {
		return "", 0, fmt.Errorf("格式错误，请输入 3 段式：分 时 周 (如: 0 8 *)")
	}

	// 禁用第 1 位（分钟位）为 * (防止每分钟高频暴击)
	if fields[0] == "*" {
		return "", 0, fmt.Errorf("分钟位不能为 '*'，请指定具体的分钟（如: 0）")
	}

	// 若只提供了 2 段（如 "0 8"），自动补全为每天（"0 8 *"）
	if len(fields) == 2 {
		cronExpr = cronExpr + " *"
	}

	// 使用 cronParser 预解析验证语法有效性
	if _, err := threeFieldCronParser.Parse(cronExpr); err != nil {
		return "", 0, fmt.Errorf("无效的 3 段式表达式: %w", err)
	}

	return cronExpr, maxJitter, nil
}

