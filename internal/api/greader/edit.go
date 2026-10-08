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

// getFormValues 获取表单或 URL Query 中的多值参数 (支持 a=1&a=2 场景)
func getFormValues(r *http.Request, key string) []string {
	_ = r.ParseForm()
	values := r.Form[key]
	if len(values) == 0 && r.URL != nil {
		values = r.URL.Query()[key]
	}
	return values
}

// EditTagHandler 接收客户端的已读/标星请求 (POST /reader/api/0/edit-tag)
// 参数: i (文章 ID, 可多个), a (add tag: 如 user/-/state/com.google/read), r (remove tag), T (action token)
func (h *Handler) EditTagHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		w.Header().Set("Google-Bad-Token", "true")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	_ = r.ParseForm()
	token := r.FormValue("T")
	if !h.checkToken(user, token) {
		w.Header().Set("Google-Bad-Token", "true")
		http.Error(w, "Unauthorized: bad action token", http.StatusUnauthorized)
		return
	}

	rawItemIDs := getFormValues(r, "i")
	addTags := getFormValues(r, "a")
	removeTags := getFormValues(r, "r")

	var articleIDs []int64
	for _, rawID := range rawItemIDs {
		id := parseArticleID(rawID)
		if id > 0 {
			articleIDs = append(articleIDs, id)
		}
	}

	if len(articleIDs) > 0 {
		var markRead, markUnread bool
		var markStarred, markUnstarred bool

		for _, tag := range addTags {
			switch tag {
			case "user/-/state/com.google/read":
				markRead = true
			case "user/-/state/com.google/starred":
				markStarred = true
			}
		}

		for _, tag := range removeTags {
			switch tag {
			case "user/-/state/com.google/read":
				markUnread = true
			case "user/-/state/com.google/starred":
				markUnstarred = true
			}
		}

		if markRead {
			_ = h.db.SetArticlesRead(user.ID, articleIDs, true)
		} else if markUnread {
			_ = h.db.SetArticlesRead(user.ID, articleIDs, false)
		}

		if markStarred {
			_ = h.db.SetArticlesStarred(user.ID, articleIDs, true)
		} else if markUnstarred {
			_ = h.db.SetArticlesStarred(user.ID, articleIDs, false)
		}
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprint(w, "OK")
}

// MarkAllAsReadHandler 处理一键全部已读 (POST /reader/api/0/mark-all-as-read)
// 参数: s (stream ID 如 feed/123 或 user/-/label/Cat 或 user/-/state/com.google/reading-list), ts (时间戳), T (token)
func (h *Handler) MarkAllAsReadHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		w.Header().Set("Google-Bad-Token", "true")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	_ = r.ParseForm()
	token := r.FormValue("T")
	if !h.checkToken(user, token) {
		w.Header().Set("Google-Bad-Token", "true")
		http.Error(w, "Unauthorized: bad action token", http.StatusUnauthorized)
		return
	}

	streamID := r.FormValue("s")
	tsStr := r.FormValue("ts")
	var ts int64
	if tsStr != "" {
		ts, _ = strconv.ParseInt(tsStr, 10, 64)
	}

	if strings.HasPrefix(streamID, "feed/") {
		if feed, err := h.findFeedByStreamID(streamID); err == nil && feed != nil {
			_ = h.db.MarkFeedAllRead(user.ID, feed.ID, ts)
		}
	} else if strings.Contains(streamID, "/label/") {
		catName := extractCategoryName(streamID)
		if cat, err := h.db.GetCategoryByName(catName); err == nil && cat != nil {
			_ = h.db.MarkCategoryAllRead(user.ID, cat.ID, ts)
		}
	} else if streamID == "user/-/state/com.google/starred" {
		_ = h.db.MarkStarredAllRead(user.ID, ts)
	} else {
		// reading-list 或全局已读
		_ = h.db.MarkAllArticlesRead(user.ID, ts)
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprint(w, "OK")
}

// RenameTagHandler 重命名标签/文件夹 (POST /reader/api/0/rename-tag 及 /reader/api/0/tag/rename)
// 参数: s (原标签名), dest (新标签名), T (token)
func (h *Handler) RenameTagHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		w.Header().Set("Google-Bad-Token", "true")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	_ = r.ParseForm()
	token := r.FormValue("T")
	if !h.checkToken(user, token) {
		w.Header().Set("Google-Bad-Token", "true")
		http.Error(w, "Unauthorized: bad action token", http.StatusUnauthorized)
		return
	}

	s := r.FormValue("s")
	dest := r.FormValue("dest")
	oldName := extractCategoryName(s)
	newName := extractCategoryName(dest)

	if oldName != "" && newName != "" {
		_ = h.db.RenameCategory(oldName, newName)
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprint(w, "OK")
}

// DisableTagHandler 删除标签/文件夹 (POST /reader/api/0/disable-tag 及 /reader/api/0/tag/delete)
// 参数: s (可多次指定), T (token)
func (h *Handler) DisableTagHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		w.Header().Set("Google-Bad-Token", "true")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	_ = r.ParseForm()
	token := r.FormValue("T")
	if !h.checkToken(user, token) {
		w.Header().Set("Google-Bad-Token", "true")
		http.Error(w, "Unauthorized: bad action token", http.StatusUnauthorized)
		return
	}

	streams := getFormValues(r, "s")
	for _, s := range streams {
		catName := extractCategoryName(s)
		if catName != "" {
			_ = h.db.DeleteCategoryByName(catName)
		}
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprint(w, "OK")
}

type QuickAddResponse struct {
	NumResults int    `json:"numResults"`
	StreamID   string `json:"streamId,omitempty"`
	StreamName string `json:"streamName,omitempty"`
	Query      string `json:"query"`
	Error      string `json:"error,omitempty"`
}

// QuickAddHandler 处理快捷添加订阅 (POST/GET /reader/api/0/subscription/quickadd)
func (h *Handler) QuickAddHandler(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()

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
			Error:      "Empty feed url",
		})
		return
	}

	feedURL := strings.TrimSpace(strings.TrimPrefix(rawQuery, "feed/"))
	feed, err := h.subscribeOrUpdateFeed(feedURL, feedURL, getFormValues(r, "a"))
	if err != nil || feed == nil {
		errStr := "Failed to subscribe"
		if err != nil {
			errStr = err.Error()
		}
		_ = json.NewEncoder(w).Encode(QuickAddResponse{
			NumResults: 0,
			Query:      rawQuery,
			Error:      errStr,
		})
		return
	}

	_ = json.NewEncoder(w).Encode(QuickAddResponse{
		NumResults: 1,
		StreamID:   fmt.Sprintf("feed/%d", feed.ID),
		StreamName: feed.Title,
		Query:      rawQuery,
	})
}

// SubscriptionEditHandler 处理订阅源增加/修改/退订 (POST /reader/api/0/subscription/edit)
// 参数: ac (subscribe / unsubscribe / edit), s (feed/123 或 feed_url), t (title), a (add tag/category), r (remove tag/category)
func (h *Handler) SubscriptionEditHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	action := r.FormValue("ac")
	streamNames := getFormValues(r, "s")
	titles := getFormValues(r, "t")
	addCategories := getFormValues(r, "a")
	removeCategories := getFormValues(r, "r")

	if len(streamNames) == 0 {
		http.Error(w, "missing stream id", http.StatusBadRequest)
		return
	}

	for i, streamID := range streamNames {
		title := ""
		if i < len(titles) {
			title = titles[i]
		}

		switch action {
		case "subscribe":
			feedURL := strings.TrimSpace(strings.TrimPrefix(streamID, "feed/"))
			if feedURL == "" {
				continue
			}
			if title == "" {
				title = feedURL
			}
			_, _ = h.subscribeOrUpdateFeed(feedURL, title, addCategories)

		case "edit":
			feed, err := h.findFeedByStreamID(streamID)
			if err == nil && feed != nil {
				// 修改标题
				if newTitle := strings.TrimSpace(title); newTitle != "" {
					feed.Title = newTitle
				}

				// 移出分类
				for _, rTag := range removeCategories {
					catName := extractCategoryName(rTag)
					if catName != "" && feed.CategoryName == catName {
						feed.CategoryID = nil
						feed.CategoryName = ""
					}
				}

				// 移入分类
				for _, aTag := range addCategories {
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
