package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"reader/internal/db"

	"golang.org/x/crypto/bcrypt"
)

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

// Login 登录接口
func (h *AdminHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req, "Invalid body") {
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

	// 为 Web 端分配专属独立的 Session Token，支持多浏览器并发登录
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

	writeJSON(w, map[string]interface{}{
		"success":    true,
		"auth_token": webToken,
		"username":   user.Username,
	})
}

// Logout 登出
func (h *AdminHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if token := currentSessionToken(r); token != "" {
		_ = h.db.DeleteUserToken(token)
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

// AuthMiddleware 认证中间件
func (h *AdminHandler) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := currentSessionToken(r)
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

		next.ServeHTTP(w, r.WithContext(setUserContext(r.Context(), user)))
	})
}

// GetProfile 获取当前登录用户个人资料
func (h *AdminHandler) GetProfile(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	writeJSON(w, map[string]interface{}{
		"username":    user.Username,
		"auth_token":  user.AuthToken,
		"server_time": time.Now().Format("2006-01-02 15:04:05"),
	})
}

// GetSessions 获取当前用户的所有活跃设备 Token / 会话列表
func (h *AdminHandler) GetSessions(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	tokens, err := h.db.GetUserTokens(user.ID, currentSessionToken(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, tokens)
}

// RevokeOtherSessions 注销除当前会话外的所有其他设备 / Token
func (h *AdminHandler) RevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	if err := h.db.RevokeOtherUserTokens(user.ID, currentSessionToken(r)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]interface{}{"success": true})
}

// RevokeSession 注销指定设备 Token / 会话
func (h *AdminHandler) RevokeSession(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	tokenID, err := parseIDParam(r, "id")
	if err != nil || tokenID <= 0 {
		http.Error(w, "Invalid token ID", http.StatusBadRequest)
		return
	}

	if err := h.db.DeleteUserTokenByID(user.ID, tokenID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]interface{}{"success": true})
}
