package greader

import (
	"crypto/rand"
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

// ClientLogin 实现 Reeder 客户端标准 Google Reader 认证
// 接收 POST x-www-form-urlencoded: Email, Passwd, service=reader
// 成功返回包含 Auth=xxx 的纯文本
func (h *Handler) ClientLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	_ = r.ParseForm()
	email := r.FormValue("Email")
	if email == "" {
		email = r.FormValue("email")
	}
	passwd := r.FormValue("Passwd")
	if passwd == "" {
		passwd = r.FormValue("passwd")
	}

	user, err := h.db.GetUserByUsername(email)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprintln(w, "Error=BadAuthentication")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(passwd)); err != nil {
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprintln(w, "Error=BadAuthentication")
		return
	}

	// 动态生成或刷新 AuthToken
	randBytes := make([]byte, 24)
	_, _ = rand.Read(randBytes)
	newToken := "reader_auth_" + hex.EncodeToString(randBytes)
	_ = h.db.AddUserToken(user.ID, newToken, "reeder")
	_ = h.db.UpdateUserToken(user.ID, newToken)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, "SID=%s\nLSID=%s\nAuth=%s\n", newToken, newToken, newToken)
}

// TokenHandler 处理 GET /reader/api/0/token (Reeder 执行 POST 修改前会请求此 token)
func (h *Handler) TokenHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprint(w, "reader-action-token-ok")
}

// AuthMiddleware 校验 Google Reader 客户端 Authorization 标头: "GoogleLogin auth=xxx"
func (h *Handler) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		token := ""

		if authHeader != "" {
			if strings.HasPrefix(authHeader, "GoogleLogin auth=") {
				token = strings.TrimPrefix(authHeader, "GoogleLogin auth=")
			} else if strings.HasPrefix(authHeader, "Bearer ") {
				token = strings.TrimPrefix(authHeader, "Bearer ")
			}
		}

		if token == "" {
			token = r.URL.Query().Get("ck")
		}

		if token == "" {
			http.Error(w, "Unauthorized: missing token", http.StatusUnauthorized)
			return
		}

		user, err := h.db.GetUserByToken(token)
		if err != nil || user == nil {
			http.Error(w, "Unauthorized: invalid token", http.StatusUnauthorized)
			return
		}

		// 存入 request context
		ctx := SetUserContext(r.Context(), user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

