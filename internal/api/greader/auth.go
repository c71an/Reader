package greader

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"reader/internal/db"
	"reader/internal/fetcher"

	"golang.org/x/crypto/bcrypt"
)

type Handler struct {
	db      *db.DB
	fetcher *fetcher.Fetcher
}

func NewHandler(database *db.DB, fetcher *fetcher.Fetcher) *Handler {
	return &Handler{db: database, fetcher: fetcher}
}

// ClientLogin 实现客户端标准 Google Reader 认证 (兼容 FreshRSS clientLogin)
// 接收 POST / GET: Email, Passwd, service=reader
// 成功返回包含 SID=, LSID=null, Auth= 的纯文本
func (h *Handler) ClientLogin(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	email := r.FormValue("Email")
	if email == "" {
		email = r.FormValue("email")
	}
	passwd := r.FormValue("Passwd")
	if passwd == "" {
		passwd = r.FormValue("passwd")
	}

	if email == "" || passwd == "" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintln(w, "Bad Request!")
		return
	}

	user, err := h.db.GetUserByUsername(email)
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprintln(w, "Error=BadAuthentication")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(passwd)); err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprintln(w, "Error=BadAuthentication")
		return
	}

	// 动态生成或刷新 AuthToken，支持多设备会话独立
	randBytes := make([]byte, 24)
	_, _ = rand.Read(randBytes)
	newToken := "reader_auth_" + hex.EncodeToString(randBytes)
	_ = h.db.AddUserToken(user.ID, newToken, "greader")
	_ = h.db.UpdateUserToken(user.ID, newToken)

	// FreshRSS ClientLogin 格式: SID=..., LSID=null, Auth=...
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, "SID=%s\nLSID=null\nAuth=%s\n", newToken, newToken)
}

// getUserActionToken 生成与 FreshRSS 兼容的 57 位 Action Token
func (h *Handler) getUserActionToken(user *db.User) string {
	hsh := sha1.Sum([]byte(fmt.Sprintf("%s:%s", user.Username, user.PasswordHash)))
	hexStr := hex.EncodeToString(hsh[:])
	// FreshRSS 要求 57 字符长度 (使用 'Z' 右补齐)
	if len(hexStr) < 57 {
		hexStr = hexStr + strings.Repeat("Z", 57-len(hexStr))
	}
	return hexStr
}

// checkToken 校验客户端提交的 action token (POST 中的 T 参数)
// 兼容 FreshRSS: 允许空、'x' (Reeder) 或与当前用户匹配的 token
func (h *Handler) checkToken(user *db.User, token string) bool {
	token = strings.TrimSpace(token)
	if token == "" || token == "x" {
		return true
	}
	expected := h.getUserActionToken(user)
	return token == expected
}

// TokenHandler 处理 GET /reader/api/0/token (客户端执行 POST 修改前请求此 action token)
func (h *Handler) TokenHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	tokenStr := "reader-action-token-okZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ"
	if user != nil {
		tokenStr = h.getUserActionToken(user)
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprintln(w, tokenStr)
}

// AuthMiddleware 校验 Google Reader 客户端 Authorization 标头或 URL 参数
func (h *Handler) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		token := ""

		if authHeader != "" {
			if strings.HasPrefix(authHeader, "GoogleLogin auth=") {
				token = strings.TrimPrefix(authHeader, "GoogleLogin auth=")
			} else if strings.HasPrefix(authHeader, "GoogleLogin_auth=") {
				token = strings.TrimPrefix(authHeader, "GoogleLogin_auth=")
			} else if strings.HasPrefix(authHeader, "Bearer ") {
				token = strings.TrimPrefix(authHeader, "Bearer ")
			} else {
				token = authHeader
			}
		}

		if token == "" {
			token = r.URL.Query().Get("ck")
		}
		if token == "" {
			token = r.URL.Query().Get("auth")
		}
		if token == "" {
			token = r.URL.Query().Get("token")
		}

		// 处理 "username/authToken" 格式
		if idx := strings.Index(token, "/"); idx != -1 {
			token = token[idx+1:]
		}

		if token == "" {
			w.Header().Set("Google-Bad-Token", "true")
			http.Error(w, "Unauthorized: missing token", http.StatusUnauthorized)
			return
		}

		user, err := h.db.GetUserByToken(token)
		if err != nil || user == nil {
			w.Header().Set("Google-Bad-Token", "true")
			http.Error(w, "Unauthorized: invalid token", http.StatusUnauthorized)
			return
		}

		// 存入 request context
		ctx := SetUserContext(r.Context(), user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
