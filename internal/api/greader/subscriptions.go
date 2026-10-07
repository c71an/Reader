package greader

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"reader/internal/db"
)

type SubscriptionItem struct {
	ID         string       `json:"id"`
	Title      string       `json:"title"`
	Categories []TagCategory `json:"categories"`
	URL        string       `json:"url"`
	HTMLURL    string       `json:"htmlUrl"`
	IconURL    string       `json:"iconUrl"`
}

type TagCategory struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type SubscriptionsResponse struct {
	Subscriptions []SubscriptionItem `json:"subscriptions"`
}

type TagItem struct {
	ID   string `json:"id"`
	SortID string `json:"sortid,omitempty"`
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

	var subs []SubscriptionItem
	for _, f := range feeds {
		var cats []TagCategory
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
			URL:        f.FeedURL,
			HTMLURL:    f.SiteURL,
		})
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(SubscriptionsResponse{Subscriptions: subs})
}

// TagListHandler 返回 GET /reader/api/0/tag/list
func (h *Handler) TagListHandler(w http.ResponseWriter, r *http.Request) {
	categories, err := h.db.GetAllCategories()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 包含系统内置标签 + 自定义分类标签
	tags := []TagItem{
		{ID: "user/-/state/com.google/starred"},
		{ID: "user/-/state/com.google/read"},
		{ID: "user/-/state/com.google/reading-list"},
	}

	for _, c := range categories {
		tags = append(tags, TagItem{
			ID: fmt.Sprintf("user/-/label/%s", c.Name),
		})
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(TagListResponse{Tags: tags})
}

// UnreadCountHandler 返回 GET /reader/api/0/unread-count
func (h *Handler) UnreadCountHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	counts, total, err := h.db.GetUnreadCounts(user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	nowUsec := time.Now().UnixNano() / 1000
	for i := range counts {
		counts[i].NewestItemTimestampUsec = nowUsec
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(UnreadCountResponse{
		Max:          total,
		UnreadCounts: counts,
	})
}

