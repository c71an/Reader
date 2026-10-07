package greader

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"reader/internal/db"
)

// EditTagHandler 接收 Reeder 的已读/标星请求
// POST /reader/api/0/edit-tag
// 参数: i (文章 ID, 可以多个), a (add tag: 如 user/-/state/com.google/read), r (remove tag)
func (h *Handler) EditTagHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	user := GetUserFromContext(r.Context())
	itemIDs := r.Form["i"]
	addTags := r.Form["a"]
	removeTags := r.Form["r"]

	for _, itemIDStr := range itemIDs {
		// 支持十进制或十六进制 (tag:google.com,2005:reader/item/0000000000000001)
		articleID := parseArticleID(itemIDStr)
		if articleID == 0 {
			continue
		}

		for _, tag := range addTags {
			switch tag {
			case "user/-/state/com.google/read":
				_ = h.db.MarkArticleRead(user.ID, articleID, true)
			case "user/-/state/com.google/starred":
				_ = h.db.MarkArticleStarred(user.ID, articleID, true)
			}
		}

		for _, tag := range removeTags {
			switch tag {
			case "user/-/state/com.google/read":
				_ = h.db.MarkArticleRead(user.ID, articleID, false)
			case "user/-/state/com.google/starred":
				_ = h.db.MarkArticleStarred(user.ID, articleID, false)
			}
		}
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprint(w, "OK")
}

// MarkAllAsReadHandler 处理一键全部已读
// POST /reader/api/0/mark-all-as-read
// 参数: s (stream ID 如 feed/123 或 user/-/state/com.google/reading-list), ts (时间戳)
func (h *Handler) MarkAllAsReadHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	user := GetUserFromContext(r.Context())
	streamID := r.FormValue("s")
	tsStr := r.FormValue("ts")
	var ts int64
	if tsStr != "" {
		// 可能是微秒
		parsed, _ := strconv.ParseInt(tsStr, 10, 64)
		if parsed > 1e12 {
			ts = parsed / 1e6
		} else {
			ts = parsed
		}
	}

	if strings.HasPrefix(streamID, "feed/") {
		feedIDStr := strings.TrimPrefix(streamID, "feed/")
		feedID, _ := strconv.ParseInt(feedIDStr, 10, 64)
		if feedID > 0 {
			_ = h.db.MarkFeedAllRead(user.ID, feedID, ts)
		}
	} else {
		_ = h.db.MarkAllArticlesRead(user.ID, ts)
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprint(w, "OK")
}

// SubscriptionEditHandler 处理订阅源增加/修改/退订
// POST /reader/api/0/subscription/edit
// 参数: ac (subscribe / unsubscribe / edit), s (feed/123 或 feed_url), t (title), a (add tag/category)
func (h *Handler) SubscriptionEditHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	action := r.FormValue("ac")
	streamID := r.FormValue("s")

	switch action {
	case "subscribe":
		feedURL := strings.TrimPrefix(streamID, "feed/")
		title := r.FormValue("t")
		if title == "" {
			title = feedURL
		}
		newFeed := &db.Feed{
			Title:         title,
			FeedURL:       feedURL,
			ScheduleType:  "interval",
			ScheduleValue: "60m",
		}
		_ = h.db.CreateFeed(newFeed)

	case "unsubscribe":
		if strings.HasPrefix(streamID, "feed/") {
			idStr := strings.TrimPrefix(streamID, "feed/")
			id, _ := strconv.ParseInt(idStr, 10, 64)
			if id > 0 {
				_ = h.db.DeleteFeed(id)
			}
		}
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprint(w, "OK")
}

func parseArticleID(raw string) int64 {
	// 如果是 "tag:google.com,2005:reader/item/000000000000000a"
	if idx := strings.LastIndex(raw, "/"); idx != -1 {
		hexStr := raw[idx+1:]
		if val, err := strconv.ParseInt(hexStr, 16, 64); err == nil {
			return val
		}
	}
	// 纯数字 ID
	if val, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return val
	}
	return 0
}

