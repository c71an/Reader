package scheduler

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"

	"reader/internal/db"
	"reader/internal/fetcher"
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
		if s.shouldFetch(feed, now) {
			s.mu.Lock()
			if s.isBusy[feed.ID] {
				s.mu.Unlock()
				continue
			}
			s.isBusy[feed.ID] = true
			s.mu.Unlock()

			fID := feed.ID
			fTitle := feed.Title
			go func() {
				// 匹配到触发时间点时，给每个源加上 10 ~ 30 秒的随机延迟（Random Jitter），削峰打散请求
				jitterSeconds := 10 + rand.IntN(21) // 10 ~ 30 秒
				jitter := time.Duration(jitterSeconds) * time.Second
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
			}()
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

// shouldFetch 核心判定函数：支持 daily_fixed (每天固定时间) 与 interval (间隔时间)，跳过 paused (暂停)
func (s *Scheduler) shouldFetch(feed *db.Feed, now time.Time) bool {
	// 如果订阅处于暂停状态，永不自动调度抓取
	if feed.ScheduleType == "paused" {
		return false
	}

	s.mu.Lock()
	if s.isBusy[feed.ID] {
		s.mu.Unlock()
		return false
	}
	lastFired := s.lastFired[feed.ID]
	s.mu.Unlock()

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

	// 添加订阅后不立即自动抓取，严格等待下一个固定或周期时间
	switch feed.ScheduleType {
	case "daily_fixed":
		// 例如 "08:00" 或 "08:00,18:30"
		return s.checkDailyFixed(feed.ScheduleValue, lastTime, now)
	case "interval":
		duration := parseInterval(feed.ScheduleValue)
		return now.Sub(lastTime) >= duration
	default:
		return false
	}
}

// checkDailyFixed 检查是否满足每日固定时间触发条件
// timesStr 格式如: "08:00", "09:30,21:00"
func (s *Scheduler) checkDailyFixed(timesStr string, lastTime, now time.Time) bool {
	if timesStr == "" {
		timesStr = "08:00"
	}
	targets := strings.Split(timesStr, ",")

	todayStr := now.Format("2006-01-02")
	for _, t := range targets {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		// 目标时间，例如 "2026-10-07 08:00"
		targetDateTimeStr := fmt.Sprintf("%s %s", todayStr, t)
		targetTime, err := time.ParseInLocation("2006-01-02 15:04", targetDateTimeStr, now.Location())
		if err != nil {
			continue
		}

		// 如果当前时间已经过了 targetTime，并且上次抓取在 targetTime 之前
		if now.After(targetTime) && lastTime.Before(targetTime) {
			return true
		}
	}
	return false
}

// parseInterval 解析如 "30m", "1h", "2h", "12h", 或纯数字(代表分钟)
func parseInterval(val string) time.Duration {
	val = strings.TrimSpace(strings.ToLower(val))
	if val == "" {
		return 1 * time.Hour
	}
	d, err := time.ParseDuration(val)
	if err == nil && d >= 5*time.Minute {
		return d
	}
	// 如果是纯数字，按分钟算
	if minutes, err := strconv.Atoi(val); err == nil && minutes > 0 {
		return time.Duration(minutes) * time.Minute
	}
	return 1 * time.Hour
}

