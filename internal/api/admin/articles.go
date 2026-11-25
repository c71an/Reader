package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"reader/internal/db"
)

// GetFeedArticles 查看指定订阅源在数据库中的文章详情
func (h *AdminHandler) GetFeedArticles(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	feedID, _ := parseIDParam(r, "id")

	feed, err := h.db.GetFeedByID(feedID)
	if err != nil {
		http.Error(w, "Feed not found", http.StatusNotFound)
		return
	}

	limit := 100
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}

	offset := 0
	if oStr := r.URL.Query().Get("offset"); oStr != "" {
		if o, err := strconv.Atoi(oStr); err == nil && o >= 0 {
			offset = o
		}
	}

	articles, total, err := h.db.GetFeedArticles(feedID, user.ID, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]interface{}{
		"feed":     feed,
		"articles": articles,
		"total":    total,
		"limit":    limit,
		"offset":   offset,
	})
}

// GetTimeline 获取时间线文章流
func (h *AdminHandler) GetTimeline(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	limit := 30
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	continuation := strings.TrimSpace(r.URL.Query().Get("continuation"))

	streamID := strings.TrimSpace(r.URL.Query().Get("stream_id"))
	if streamID == "" {
		streamID = "user/-/state/com.google/reading-list"
	}

	excludePaused := r.URL.Query().Get("include_paused") != "true"

	filter := db.StreamQueryFilter{
		UserID:        user.ID,
		StreamID:      streamID,
		Limit:         limit,
		Continuation:  continuation,
		ExcludePaused: excludePaused,
	}

	if r.URL.Query().Get("unread_only") == "true" {
		filter.ExcludeTarget = "user/-/state/com.google/read"
	}
	if r.URL.Query().Get("starred_only") == "true" {
		filter.FilterTarget = "user/-/state/com.google/starred"
	}

	items, err := h.db.GetStreamItems(filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if items == nil {
		items = []*db.StreamItem{}
	}

	var nextContinuation string
	if len(items) == limit {
		last := items[len(items)-1]
		nextContinuation = fmt.Sprintf("%d_%d", last.PublishedAt.Unix(), last.ID)
	}

	writeJSON(w, map[string]interface{}{
		"items":        items,
		"continuation": nextContinuation,
		"limit":        limit,
	})
}

// BatchMarkArticlesRead 批量设置文章已读/未读状态
func (h *AdminHandler) BatchMarkArticlesRead(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	var req struct {
		ArticleIDs []int64 `json:"article_ids"`
		IsRead     bool    `json:"is_read"`
	}
	if !decodeJSON(w, r, &req, "Invalid body") {
		return
	}

	if err := h.db.SetArticlesRead(user.ID, req.ArticleIDs, req.IsRead); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]interface{}{
		"success":  true,
		"affected": len(req.ArticleIDs),
		"is_read":  req.IsRead,
	})
}

// BatchMarkArticlesStarred 批量设置文章星标状态
func (h *AdminHandler) BatchMarkArticlesStarred(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	var req struct {
		ArticleIDs []int64 `json:"article_ids"`
		IsStarred  bool    `json:"is_starred"`
	}
	if !decodeJSON(w, r, &req, "Invalid body") {
		return
	}

	if err := h.db.SetArticlesStarred(user.ID, req.ArticleIDs, req.IsStarred); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]interface{}{
		"success":    true,
		"affected":   len(req.ArticleIDs),
		"is_starred": req.IsStarred,
	})
}

// MarkAllTimelineRead 一键标记全流为已读
func (h *AdminHandler) MarkAllTimelineRead(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	var req struct {
		StreamID string `json:"stream_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	sID := strings.TrimSpace(req.StreamID)
	nowTs := time.Now().Unix()

	var err error
	if strings.HasPrefix(sID, "feed/") {
		feedTarget := strings.TrimPrefix(sID, "feed/")
		if id, pErr := strconv.ParseInt(feedTarget, 10, 64); pErr == nil && id > 0 {
			err = h.db.MarkFeedAllRead(user.ID, id, nowTs)
		}
	} else if idx := strings.Index(sID, "/label/"); idx != -1 {
		categoryName := strings.TrimSpace(sID[idx+len("/label/"):])
		if cat, cErr := h.db.GetCategoryByName(categoryName); cErr == nil && cat != nil {
			err = h.db.MarkCategoryAllRead(user.ID, cat.ID, nowTs)
		}
	} else {
		err = h.db.MarkAllArticlesRead(user.ID, nowTs)
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]interface{}{
		"success": true,
	})
}
