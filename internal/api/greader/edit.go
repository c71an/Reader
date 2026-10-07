package greader

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"reader/internal/db"
)

// EditTagHandler 接收 Reeder 的已读/标星请求
// POST /reader/api/0/edit-tag
// 参数: i (文章 ID, 可以多个), a (add tag: 如 user/-/state/com.google/read), r (remove tag)
func (h *Handler) EditTagHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

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
	user := GetUserFromContext(r.Context())
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

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
		if feed, err := h.findFeedByStreamID(streamID); err == nil && feed != nil {
			_ = h.db.MarkFeedAllRead(user.ID, feed.ID, ts)
		}
	} else {
		_ = h.db.MarkAllArticlesRead(user.ID, ts)
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprint(w, "OK")
}

type QuickAddResponse struct {
	NumResults int    `json:"numResults"`
	StreamID   string `json:"streamId,omitempty"`
	Query      string `json:"query"`
}

// QuickAddHandler 处理 iOS Reeder 客户端快捷添加订阅
// POST/GET /reader/api/0/subscription/quickadd
// 参数: quickadd (URL) 或 url (URL)
func (h *Handler) QuickAddHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	rawQuery := strings.TrimSpace(r.FormValue("quickadd"))
	if rawQuery == "" {
		rawQuery = strings.TrimSpace(r.FormValue("url"))
	}
	if rawQuery == "" {
		rawQuery = strings.TrimSpace(r.URL.Query().Get("quickadd"))
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if rawQuery == "" {
		_ = json.NewEncoder(w).Encode(QuickAddResponse{
			NumResults: 0,
			Query:      rawQuery,
		})
		return
	}

	feedURL := strings.TrimSpace(strings.TrimPrefix(rawQuery, "feed/"))
	feed, err := h.subscribeOrUpdateFeed(feedURL, feedURL, r.Form["a"])
	if err != nil || feed == nil {
		_ = json.NewEncoder(w).Encode(QuickAddResponse{
			NumResults: 0,
			Query:      rawQuery,
		})
		return
	}

	_ = json.NewEncoder(w).Encode(QuickAddResponse{
		NumResults: 1,
		StreamID:   fmt.Sprintf("feed/%d", feed.ID),
		Query:      rawQuery,
	})
}

// SubscriptionEditHandler 处理订阅源增加/修改/退订
// POST /reader/api/0/subscription/edit
// 参数: ac (subscribe / unsubscribe / edit), s (feed/123 或 feed_url), t (title), a (add tag/category), r (remove tag/category)
func (h *Handler) SubscriptionEditHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	action := r.FormValue("ac")
	streamID := r.FormValue("s")

	switch action {
	case "subscribe":
		feedURL := strings.TrimSpace(strings.TrimPrefix(streamID, "feed/"))
		if feedURL == "" {
			http.Error(w, "missing stream id", http.StatusBadRequest)
			return
		}

		title := strings.TrimSpace(r.FormValue("t"))
		if title == "" {
			title = feedURL
		}

		_, _ = h.subscribeOrUpdateFeed(feedURL, title, r.Form["a"])

	case "edit":
		feed, err := h.findFeedByStreamID(streamID)
		if err == nil && feed != nil {
			// 修改标题
			if newTitle := strings.TrimSpace(r.FormValue("t")); newTitle != "" {
				feed.Title = newTitle
			}

			// 移出分类
			for _, rTag := range r.Form["r"] {
				catName := extractCategoryName(rTag)
				if catName != "" && feed.CategoryName == catName {
					feed.CategoryID = nil
					feed.CategoryName = ""
				}
			}

			// 移入分类
			for _, aTag := range r.Form["a"] {
				catName := extractCategoryName(aTag)
				if catName != "" {
					if cat, err := h.db.GetOrCreateCategory(catName); err == nil && cat != nil {
						feed.CategoryID = &cat.ID
						feed.CategoryName = cat.Name
						break
					}
				}
			}

			_ = h.db.UpdateFeed(feed)
		}

	case "unsubscribe":
		if feed, err := h.findFeedByStreamID(streamID); err == nil && feed != nil {
			_ = h.db.DeleteFeed(feed.ID)
		}
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprint(w, "OK")
}

func (h *Handler) resolveCategoryFromTags(tags []string) *int64 {
	for _, aTag := range tags {
		catName := extractCategoryName(aTag)
		if catName != "" {
			if cat, err := h.db.GetOrCreateCategory(catName); err == nil && cat != nil {
				return &cat.ID
			}
		}
	}
	return nil
}

func (h *Handler) subscribeOrUpdateFeed(feedURL, title string, categoryTags []string) (*db.Feed, error) {
	if feedURL == "" {
		return nil, fmt.Errorf("empty feed url")
	}

	catID := h.resolveCategoryFromTags(categoryTags)

	existingFeed, err := h.db.GetFeedByURL(feedURL)
	if err == nil && existingFeed != nil {
		updated := false
		if title != "" && title != feedURL && existingFeed.Title != title {
			existingFeed.Title = title
			updated = true
		}
		if catID != nil && (existingFeed.CategoryID == nil || *existingFeed.CategoryID != *catID) {
			existingFeed.CategoryID = catID
			updated = true
		}
		if updated {
			_ = h.db.UpdateFeed(existingFeed)
		}
		return existingFeed, nil
	}

	newFeed := &db.Feed{
		Title:         title,
		FeedURL:       feedURL,
		CategoryID:    catID,
		ScheduleType:  "interval",
		ScheduleValue: "60m",
	}
	if err := h.db.CreateFeed(newFeed); err != nil {
		return nil, err
	}

	// 及时进行首次抓取，获取真实标题、网址与首批文章
	if h.fetcher != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _ = h.fetcher.FetchFeed(ctx, newFeed)
		cancel()
	}

	return newFeed, nil
}

func (h *Handler) findFeedByStreamID(streamID string) (*db.Feed, error) {
	raw := strings.TrimPrefix(streamID, "feed/")
	if id, err := strconv.ParseInt(raw, 10, 64); err == nil && id > 0 {
		f, err := h.db.GetFeedByID(id)
		if err == nil && f != nil {
			return f, nil
		}
	}
	f, err := h.db.GetFeedByURL(raw)
	if err == nil && f != nil {
		return f, nil
	}
	if raw != streamID {
		f, err = h.db.GetFeedByURL(streamID)
		if err == nil && f != nil {
			return f, nil
		}
	}
	return nil, fmt.Errorf("feed not found")
}

func extractCategoryName(tag string) string {
	tag = strings.TrimSpace(tag)
	if strings.Contains(tag, "/state/") {
		return ""
	}
	if idx := strings.Index(tag, "/label/"); idx != -1 {
		return strings.TrimSpace(tag[idx+len("/label/"):])
	}
	if !strings.Contains(tag, "/") {
		return tag
	}
	return ""
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

