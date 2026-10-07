package admin

import (
	"context"
	"encoding/json"
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

	http.SetCookie(w, &http.Cookie{
		Name:     "reader_token",
		Value:    user.AuthToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400 * 30,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"auth_token": user.AuthToken,
		"username":   user.Username,
	})
}

// 登出
func (h *AdminHandler) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "reader_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	w.WriteHeader(http.StatusOK)
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

		ctx := context.WithValue(r.Context(), "user", user)
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

	if req.ScheduleType != "daily_fixed" && req.ScheduleType != "interval" {
		req.ScheduleType = "interval"
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

	// 触发后台首次抓取
	go h.scheduler.FetchNow(feed.ID)

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
		"success":     true,
		"new_articles": count,
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

// 获取个人设置 (包含 Reeder 凭证及接入指引)
func (h *AdminHandler) GetProfile(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value("user").(*db.User)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"username":   user.Username,
		"auth_token": user.AuthToken,
		"server_time": time.Now().Format("2006-01-02 15:04:05"),
	})
}

