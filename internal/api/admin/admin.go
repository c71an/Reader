package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"reader/internal/db"
	"reader/internal/scheduler"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
)

type AdminHandler struct {
	db        *db.DB
	scheduler *scheduler.Scheduler
}

func NewAdminHandler(database *db.DB, sched *scheduler.Scheduler) *AdminHandler {
	return &AdminHandler{
		db:        database,
		scheduler: sched,
	}
}

// 登录接口
func (h *AdminHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}

	user, err := h.db.GetUserByUsername(req.Username)
	if err != nil {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	// 为 Web 端分配专属独立的 Session Token，避免多设备/Reeder 互相挤下线
	randBytes := make([]byte, 24)
	_, _ = rand.Read(randBytes)
	webToken := "web_session_" + hex.EncodeToString(randBytes)
	_ = h.db.AddUserToken(user.ID, webToken, "web")

	http.SetCookie(w, &http.Cookie{
		Name:     "reader_token",
		Value:    webToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400 * 30,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"auth_token": webToken,
		"username":   user.Username,
	})
}

// 登出
func (h *AdminHandler) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("reader_token")
	if err == nil && cookie.Value != "" {
		_ = h.db.DeleteUserToken(cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "reader_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	w.WriteHeader(http.StatusOK)
}

type contextKey string

const adminUserKey contextKey = "admin_user"

func setUserContext(ctx context.Context, user *db.User) context.Context {
	return context.WithValue(ctx, adminUserKey, user)
}

func getUserFromContext(ctx context.Context) *db.User {
	if u, ok := ctx.Value(adminUserKey).(*db.User); ok {
		return u
	}
	return nil
}

// 认证中间件
func (h *AdminHandler) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ""
		cookie, err := r.Cookie("reader_token")
		if err == nil {
			token = cookie.Value
		}
		if token == "" {
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				token = strings.TrimPrefix(authHeader, "Bearer ")
			}
		}

		if token == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		user, err := h.db.GetUserByToken(token)
		if err != nil || user == nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		ctx := setUserContext(r.Context(), user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// 获取全部订阅
func (h *AdminHandler) GetFeeds(w http.ResponseWriter, r *http.Request) {
	feeds, err := h.db.GetAllFeeds()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(feeds)
}

// 创建新订阅
func (h *AdminHandler) CreateFeed(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title         string `json:"title"`
		FeedURL       string `json:"feed_url"`
		CategoryName  string `json:"category_name"`
		ScheduleType  string `json:"schedule_type"`  // "daily_fixed" | "interval"
		ScheduleValue string `json:"schedule_value"` // e.g. "08:00,18:30" or "60m"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	req.FeedURL = strings.TrimSpace(req.FeedURL)
	if req.FeedURL == "" {
		http.Error(w, "feed_url is required", http.StatusBadRequest)
		return
	}

	if req.ScheduleType != "daily_fixed" && req.ScheduleType != "interval" && req.ScheduleType != "paused" {
		req.ScheduleType = "daily_fixed"
	}
	if req.ScheduleValue == "" {
		if req.ScheduleType == "daily_fixed" {
			req.ScheduleValue = "08:00"
		} else {
			req.ScheduleValue = "60m"
		}
	}

	var catID *int64
	if strings.TrimSpace(req.CategoryName) != "" {
		cat, err := h.db.GetOrCreateCategory(strings.TrimSpace(req.CategoryName))
		if err == nil && cat != nil {
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

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(feed)
}

// 修改订阅配置
func (h *AdminHandler) UpdateFeed(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.ParseInt(idStr, 10, 64)

	feed, err := h.db.GetFeedByID(id)
	if err != nil {
		http.Error(w, "Feed not found", http.StatusNotFound)
		return
	}

	var req struct {
		Title         string `json:"title"`
		FeedURL       string `json:"feed_url"`
		CategoryName  string `json:"category_name"`
		ScheduleType  string `json:"schedule_type"`
		ScheduleValue string `json:"schedule_value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.Title != "" {
		feed.Title = req.Title
	}
	if req.FeedURL != "" {
		feed.FeedURL = req.FeedURL
	}
	if req.ScheduleType != "" {
		feed.ScheduleType = req.ScheduleType
	}
	if req.ScheduleValue != "" {
		feed.ScheduleValue = req.ScheduleValue
	}

	if req.CategoryName != "" {
		cat, err := h.db.GetOrCreateCategory(strings.TrimSpace(req.CategoryName))
		if err == nil && cat != nil {
			feed.CategoryID = &cat.ID
		}
	}

	if err := h.db.UpdateFeed(feed); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(feed)
}

// 删除订阅
func (h *AdminHandler) DeleteFeed(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.ParseInt(idStr, 10, 64)

	if err := h.db.DeleteFeed(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// 手动立即拉取
func (h *AdminHandler) FetchFeedNow(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.ParseInt(idStr, 10, 64)

	count, err := h.scheduler.FetchNow(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      true,
		"new_articles": count,
	})
}

// 切换单个订阅暂停/恢复状态
func (h *AdminHandler) ToggleFeedPause(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.ParseInt(idStr, 10, 64)

	feed, err := h.db.GetFeedByID(id)
	if err != nil {
		http.Error(w, "Feed not found", http.StatusNotFound)
		return
	}

	if feed.ScheduleType == "paused" {
		feed.ScheduleType = "daily_fixed"
		if feed.ScheduleValue == "" {
			feed.ScheduleValue = "08:00"
		}
	} else {
		feed.ScheduleType = "paused"
	}

	if err := h.db.UpdateFeed(feed); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(feed)
}

// 批量操作接口 (支持 action: pause, resume, delete, fetch)
func (h *AdminHandler) BatchAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action  string  `json:"action"` // "pause", "resume", "delete", "fetch"
		FeedIDs []int64 `json:"feed_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}

	totalFetched := 0
	for _, feedID := range req.FeedIDs {
		switch req.Action {
		case "pause":
			feed, err := h.db.GetFeedByID(feedID)
			if err == nil && feed != nil {
				feed.ScheduleType = "paused"
				_ = h.db.UpdateFeed(feed)
			}
		case "resume":
			feed, err := h.db.GetFeedByID(feedID)
			if err == nil && feed != nil {
				if feed.ScheduleType == "paused" {
					feed.ScheduleType = "daily_fixed"
					if feed.ScheduleValue == "" {
						feed.ScheduleValue = "08:00"
					}
					_ = h.db.UpdateFeed(feed)
				}
			}
		case "delete":
			_ = h.db.DeleteFeed(feedID)
		case "fetch":
			count, err := h.scheduler.FetchNow(feedID)
			if err == nil {
				totalFetched += count
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"action":        req.Action,
		"affected":      len(req.FeedIDs),
		"total_fetched": totalFetched,
	})
}

// 自动均匀分配交错时间 (Staggered Schedule)
func (h *AdminHandler) DistributeSchedule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FeedIDs       []int64 `json:"feed_ids"`
		StartTime     string  `json:"start_time"`     // 格式如 "08:00"
		WindowMinutes int     `json:"window_minutes"` // 窗口跨度分钟数，如 30, 60
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}

	if len(req.FeedIDs) == 0 {
		http.Error(w, "No feed_ids provided", http.StatusBadRequest)
		return
	}

	startHour, startMin := 8, 0
	if req.StartTime != "" {
		parts := strings.Split(req.StartTime, ":")
		if len(parts) == 2 {
			if hVal, err := strconv.Atoi(strings.TrimSpace(parts[0])); err == nil {
				startHour = hVal
			}
			if mVal, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
				startMin = mVal
			}
		}
	}

	window := req.WindowMinutes
	if window <= 0 {
		window = 30
	}

	n := len(req.FeedIDs)
	var step float64
	if n > 1 {
		step = float64(window) / float64(n)
	}

	updatedList := make([]map[string]interface{}, 0, n)
	for i, feedID := range req.FeedIDs {
		feed, err := h.db.GetFeedByID(feedID)
		if err != nil || feed == nil {
			continue
		}

		offsetMinutes := int(float64(i) * step)
		totalMin := (startHour*60 + startMin + offsetMinutes) % 1440
		targetHour := totalMin / 60
		targetMin := totalMin % 60
		timeStr := fmt.Sprintf("%02d:%02d", targetHour, targetMin)

		feed.ScheduleType = "daily_fixed"
		feed.ScheduleValue = timeStr
		if err := h.db.UpdateFeed(feed); err == nil {
			updatedList = append(updatedList, map[string]interface{}{
				"id":             feed.ID,
				"title":          feed.Title,
				"schedule_type":  feed.ScheduleType,
				"schedule_value": feed.ScheduleValue,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"affected": len(updatedList),
		"feeds":    updatedList,
	})
}

// 获取分类列表
func (h *AdminHandler) GetCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := h.db.GetAllCategories()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cats)
}

// 修改分类文件夹标题与排列顺序
func (h *AdminHandler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentName string `json:"current_name"`
		Name        string `json:"name"`
		SortOrder   int    `json:"sort_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}

	if err := h.db.UpdateCategoryByName(req.CurrentName, req.Name, req.SortOrder); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

// 获取个人设置 (包含 Reeder 凭证及接入指引)
func (h *AdminHandler) GetProfile(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r.Context())
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"username":    user.Username,
		"auth_token":  user.AuthToken,
		"server_time": time.Now().Format("2006-01-02 15:04:05"),
	})
}

// 查看指定订阅源在数据库中的文章详情
func (h *AdminHandler) GetFeedArticles(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r.Context())
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	idStr := chi.URLParam(r, "id")
	feedID, _ := strconv.ParseInt(idStr, 10, 64)

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

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"feed":     feed,
		"articles": articles,
		"total":    total,
		"limit":    limit,
		"offset":   offset,
	})
}

// 批量设置文章已读/未读状态
func (h *AdminHandler) BatchMarkArticlesRead(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r.Context())
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		ArticleIDs []int64 `json:"article_ids"`
		IsRead     bool    `json:"is_read"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}

	if err := h.db.SetArticlesRead(user.ID, req.ArticleIDs, req.IsRead); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"affected": len(req.ArticleIDs),
		"is_read":  req.IsRead,
	})
}


