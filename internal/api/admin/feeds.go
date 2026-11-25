package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"reader/internal/db"
	"reader/internal/scheduler"
)

// GetFeeds 获取全部订阅
func (h *AdminHandler) GetFeeds(w http.ResponseWriter, r *http.Request) {
	feeds, err := h.db.GetAllFeeds()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, feeds)
}

// CreateFeed 创建新订阅
func (h *AdminHandler) CreateFeed(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title         string `json:"title"`
		FeedURL       string `json:"feed_url"`
		CategoryName  string `json:"category_name"`
		ScheduleType  string `json:"schedule_type"`  // "cron" | "paused"
		ScheduleValue string `json:"schedule_value"` // e.g. "0 8 * ~30m", "0 10 0"
	}
	if !decodeJSON(w, r, &req, "") {
		return
	}

	req.FeedURL = strings.TrimSpace(req.FeedURL)
	if req.FeedURL == "" {
		http.Error(w, "feed_url is required", http.StatusBadRequest)
		return
	}

	if req.ScheduleType != "paused" {
		req.ScheduleType = "cron"
	}
	if req.ScheduleValue == "" {
		req.ScheduleValue = "0 8 * ~30m"
	}
	if _, _, err := scheduler.ParseScheduleWithJitter(req.ScheduleValue); err != nil {
		http.Error(w, fmt.Sprintf("invalid schedule: %v", err), http.StatusBadRequest)
		return
	}

	var catID *int64
	if catName := strings.TrimSpace(req.CategoryName); catName != "" {
		if cat, err := h.db.GetOrCreateCategory(catName); err == nil && cat != nil {
			catID = &cat.ID
		}
	}

	feed := &db.Feed{
		Title:         req.Title,
		FeedURL:       req.FeedURL,
		CategoryID:    catID,
		ScheduleType:  req.ScheduleType,
		ScheduleValue: req.ScheduleValue,
	}

	if err := h.db.CreateFeed(feed); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 添加订阅后不自动抓取刷新，严格等待下一个定时时间再抓取
	writeJSON(w, feed)
}

// UpdateFeed 修改订阅配置
func (h *AdminHandler) UpdateFeed(w http.ResponseWriter, r *http.Request) {
	id, _ := parseIDParam(r, "id")

	feed, err := h.db.GetFeedByID(id)
	if err != nil {
		http.Error(w, "Feed not found", http.StatusNotFound)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req struct {
		Title         string  `json:"title"`
		FeedURL       string  `json:"feed_url"`
		CategoryName  *string `json:"category_name"`
		ScheduleType  string  `json:"schedule_type"`
		ScheduleValue string  `json:"schedule_value"`
	}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.Title != "" {
		feed.Title = req.Title
	}
	if req.FeedURL != "" {
		feed.FeedURL = req.FeedURL
	}

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(bodyBytes, &rawMap); err == nil {
		if raw, ok := rawMap["max_articles"]; ok {
			trimmed := strings.TrimSpace(string(raw))
			if trimmed == "null" || trimmed == "" {
				feed.MaxArticles = nil
			} else {
				var val int
				if err := json.Unmarshal(raw, &val); err == nil {
					if val < 0 {
						feed.MaxArticles = nil
					} else {
						feed.MaxArticles = &val
					}
				}
			}
		}
	}

	if req.ScheduleType != "" {
		if req.ScheduleType != "paused" {
			feed.ScheduleType = "cron"
		} else {
			feed.ScheduleType = "paused"
		}
	}
	if req.ScheduleValue != "" {
		if _, _, err := scheduler.ParseScheduleWithJitter(req.ScheduleValue); err != nil {
			http.Error(w, fmt.Sprintf("invalid schedule: %v", err), http.StatusBadRequest)
			return
		}
		feed.ScheduleValue = req.ScheduleValue
	}

	if req.CategoryName != nil {
		trimmed := strings.TrimSpace(*req.CategoryName)
		if trimmed == "" {
			feed.CategoryID = nil
		} else if cat, err := h.db.GetOrCreateCategory(trimmed); err == nil && cat != nil {
			feed.CategoryID = &cat.ID
		}
	}

	if err := h.db.UpdateFeed(feed); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 如果设置了最大文章数限制（>= 0），保存后立即清理超出的旧文章；若为 0 则清空
	if feed.MaxArticles != nil && *feed.MaxArticles >= 0 {
		_, _ = h.db.TrimFeedArticles(feed.ID, *feed.MaxArticles)
	}

	writeJSON(w, feed)
}

// DeleteFeed 删除订阅
func (h *AdminHandler) DeleteFeed(w http.ResponseWriter, r *http.Request) {
	id, _ := parseIDParam(r, "id")

	if err := h.db.DeleteFeed(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// FetchFeedNow 手动立即拉取
func (h *AdminHandler) FetchFeedNow(w http.ResponseWriter, r *http.Request) {
	id, _ := parseIDParam(r, "id")

	count, err := h.scheduler.FetchNow(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]interface{}{
		"success":      true,
		"new_articles": count,
	})
}

// ToggleFeedPause 切换单个订阅暂停/恢复状态
func (h *AdminHandler) ToggleFeedPause(w http.ResponseWriter, r *http.Request) {
	id, _ := parseIDParam(r, "id")

	feed, err := h.db.GetFeedByID(id)
	if err != nil {
		http.Error(w, "Feed not found", http.StatusNotFound)
		return
	}

	if feed.ScheduleType == "paused" {
		feed.ScheduleType = "cron"
		if feed.ScheduleValue == "" {
			feed.ScheduleValue = "0 8 * ~30m"
		}
	} else {
		feed.ScheduleType = "paused"
	}

	if err := h.db.UpdateFeed(feed); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, feed)
}

// BatchAction 批量操作接口 (支持 action: pause, resume, delete, fetch)
func (h *AdminHandler) BatchAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action  string  `json:"action"` // "pause", "resume", "delete", "fetch"
		FeedIDs []int64 `json:"feed_ids"`
	}
	if !decodeJSON(w, r, &req, "Invalid body") {
		return
	}

	totalFetched := 0
	for _, feedID := range req.FeedIDs {
		switch req.Action {
		case "pause":
			if feed, err := h.db.GetFeedByID(feedID); err == nil && feed != nil {
				feed.ScheduleType = "paused"
				_ = h.db.UpdateFeed(feed)
			}
		case "resume":
			if feed, err := h.db.GetFeedByID(feedID); err == nil && feed != nil && feed.ScheduleType == "paused" {
				feed.ScheduleType = "cron"
				if feed.ScheduleValue == "" {
					feed.ScheduleValue = "0 8 * ~30m"
				}
				_ = h.db.UpdateFeed(feed)
			}
		case "delete":
			_ = h.db.DeleteFeed(feedID)
		case "fetch":
			if count, err := h.scheduler.FetchNow(feedID); err == nil {
				totalFetched += count
			}
		}
	}

	writeJSON(w, map[string]interface{}{
		"success":       true,
		"action":        req.Action,
		"affected":      len(req.FeedIDs),
		"total_fetched": totalFetched,
	})
}

// DistributeSchedule 批量错峰时间分配
func (h *AdminHandler) DistributeSchedule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FeedIDs         []int64 `json:"feed_ids"`
		StartTime       string  `json:"start_time"`       // 格式如 "08:00"
		IntervalMinutes int     `json:"interval_minutes"` // 递增步长间隔(分钟)，如 0, 1, 2
		Week            string  `json:"week"`             // 周期表达式，如 "*", "1-5", "0,6"
		Jitter          string  `json:"jitter"`           // 浮动时长，如 "30m", 留空则无
	}
	if !decodeJSON(w, r, &req, "Invalid body") {
		return
	}

	if len(req.FeedIDs) == 0 {
		http.Error(w, "No feed_ids provided", http.StatusBadRequest)
		return
	}

	schedules, err := scheduler.BuildStaggeredSchedules(
		len(req.FeedIDs), req.StartTime, req.IntervalMinutes, req.Week, req.Jitter,
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid schedule: %v", err), http.StatusBadRequest)
		return
	}

	updatedList := make([]map[string]interface{}, 0, len(req.FeedIDs))
	for i, feedID := range req.FeedIDs {
		feed, err := h.db.GetFeedByID(feedID)
		if err != nil || feed == nil {
			continue
		}

		feed.ScheduleType = "cron"
		feed.ScheduleValue = schedules[i]
		if err := h.db.UpdateFeed(feed); err == nil {
			updatedList = append(updatedList, map[string]interface{}{
				"id":             feed.ID,
				"title":          feed.Title,
				"schedule_type":  feed.ScheduleType,
				"schedule_value": feed.ScheduleValue,
			})
		}
	}

	writeJSON(w, map[string]interface{}{
		"success":  true,
		"affected": len(updatedList),
		"feeds":    updatedList,
	})
}

// GetCategories 获取分类列表
func (h *AdminHandler) GetCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := h.db.GetAllCategories()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, cats)
}

// UpdateCategory 修改分类文件夹标题与排列顺序
func (h *AdminHandler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentName string `json:"current_name"`
		Name        string `json:"name"`
		SortOrder   int    `json:"sort_order"`
	}
	if !decodeJSON(w, r, &req, "Invalid body") {
		return
	}

	if err := h.db.UpdateCategoryByName(req.CurrentName, req.Name, req.SortOrder); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]interface{}{
		"success": true,
	})
}
