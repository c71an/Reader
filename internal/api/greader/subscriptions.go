package greader

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"

	"reader/internal/db"
)

type SubscriptionItem struct {
	ID            string        `json:"id"`
	Title         string        `json:"title"`
	Categories    []TagCategory `json:"categories"`
	URL           string        `json:"url"`
	HTMLURL       string        `json:"htmlUrl"`
	IconURL       string        `json:"iconUrl,omitempty"`
	SortID        string        `json:"sortid,omitempty"`
	FirstItemMsec int64         `json:"firstitemmsec,omitempty"`
}

type TagCategory struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type SubscriptionsResponse struct {
	Subscriptions []SubscriptionItem `json:"subscriptions"`
}

type TagItem struct {
	ID          string `json:"id"`
	SortID      string `json:"sortid,omitempty"`
	Type        string `json:"type,omitempty"`        // FreshRSS / Inoreader: "folder" 或 "tag"
	UnreadCount int    `json:"unread_count,omitempty"` // Inoreader 扩展
}

type TagListResponse struct {
	Tags []TagItem `json:"tags"`
}

type UnreadCountResponse struct {
	Max          int                  `json:"max"`
	UnreadCounts []db.UnreadCountItem `json:"unreadcounts"`
}

type UserInfoResponse struct {
	UserID        string `json:"userId"`
	UserName      string `json:"userName"`
	UserProfileID string `json:"userProfileId"`
	UserEmail     string `json:"userEmail"`
}

// UserInfoHandler 返回 GET /reader/api/0/user-info
func (h *Handler) UserInfoHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		w.Header().Set("Google-Bad-Token", "true")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(UserInfoResponse{
		UserID:        fmt.Sprintf("%d", user.ID),
		UserName:      user.Username,
		UserProfileID: fmt.Sprintf("%d", user.ID),
		UserEmail:     user.Username,
	})
}

// SubscriptionListHandler 返回 GET /reader/api/0/subscription/list
func (h *Handler) SubscriptionListHandler(w http.ResponseWriter, r *http.Request) {
	feeds, err := h.db.GetAllFeeds()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	subs := make([]SubscriptionItem, 0, len(feeds))
	for _, f := range feeds {
		cats := make([]TagCategory, 0)
		if f.CategoryName != "" {
			cats = append(cats, TagCategory{
				ID:    fmt.Sprintf("user/-/label/%s", f.CategoryName),
				Label: f.CategoryName,
			})
		}

		subs = append(subs, SubscriptionItem{
			ID:         fmt.Sprintf("feed/%d", f.ID),
			Title:      f.Title,
			Categories: cats,
			SortID:     fmt.Sprintf("%08x", f.ID),
			URL:        f.FeedURL,
			HTMLURL:    f.SiteURL,
		})
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(SubscriptionsResponse{Subscriptions: subs})
}

// TagListHandler 返回 GET /reader/api/0/tag/list (兼容 FreshRSS)
func (h *Handler) TagListHandler(w http.ResponseWriter, r *http.Request) {
	categories, err := h.db.GetAllCategories()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 包含 FreshRSS 标准系统内置状态标签 + 用户分类文件夹
	tags := []TagItem{
		{ID: "user/-/state/com.google/starred"},
		{ID: "user/-/state/com.google/reading-list"},
		{ID: "user/-/state/org.freshrss/main"},
		{ID: "user/-/state/org.freshrss/important"},
	}

	for _, c := range categories {
		tags = append(tags, TagItem{
			ID:     fmt.Sprintf("user/-/label/%s", c.Name),
			Type:   "folder", // 标识为文件夹目录
			SortID: fmt.Sprintf("%d", c.SortOrder),
		})
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(TagListResponse{Tags: tags})
}

// UnreadCountHandler 返回 GET /reader/api/0/unread-count
func (h *Handler) UnreadCountHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		w.Header().Set("Google-Bad-Token", "true")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	counts, total, err := h.db.GetUnreadCounts(user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(UnreadCountResponse{
		Max:          total,
		UnreadCounts: counts,
	})
}

// SubscriptionExportHandler 导出 OPML 文件 (GET /reader/api/0/subscription/export)
func (h *Handler) SubscriptionExportHandler(w http.ResponseWriter, r *http.Request) {
	feeds, err := h.db.GetAllFeeds()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 按分类对 feeds 进行归类
	categoriesMap := make(map[string][]*db.Feed)
	var uncategorized []*db.Feed

	for _, f := range feeds {
		if f.CategoryName != "" {
			categoriesMap[f.CategoryName] = append(categoriesMap[f.CategoryName], f)
		} else {
			uncategorized = append(uncategorized, f)
		}
	}

	var sb strings.Builder
	sb.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	sb.WriteString("<opml version=\"2.0\">\n")
	sb.WriteString("  <head>\n")
	sb.WriteString("    <title>Reader Subscriptions</title>\n")
	sb.WriteString("  </head>\n")
	sb.WriteString("  <body>\n")

	for catName, catFeeds := range categoriesMap {
		sb.WriteString(fmt.Sprintf("    <outline text=\"%s\" title=\"%s\">\n", html.EscapeString(catName), html.EscapeString(catName)))
		for _, f := range catFeeds {
			sb.WriteString(fmt.Sprintf("      <outline type=\"rss\" text=\"%s\" title=\"%s\" xmlUrl=\"%s\" htmlUrl=\"%s\"/>\n",
				html.EscapeString(f.Title), html.EscapeString(f.Title), html.EscapeString(f.FeedURL), html.EscapeString(f.SiteURL)))
		}
		sb.WriteString("    </outline>\n")
	}

	for _, f := range uncategorized {
		sb.WriteString(fmt.Sprintf("    <outline type=\"rss\" text=\"%s\" title=\"%s\" xmlUrl=\"%s\" htmlUrl=\"%s\"/>\n",
			html.EscapeString(f.Title), html.EscapeString(f.Title), html.EscapeString(f.FeedURL), html.EscapeString(f.SiteURL)))
	}

	sb.WriteString("  </body>\n")
	sb.WriteString("</opml>\n")

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\"reader-subscriptions.opml\"")
	_, _ = w.Write([]byte(sb.String()))
}
