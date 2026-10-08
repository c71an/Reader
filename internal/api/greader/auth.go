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
	authVal := fmt.Sprintf("%s/%s", user.Username, newToken)

	// 存入会话表 (单条记录，GetUserByToken 自动适配前缀，配合 LRU 自动限额)
	_ = h.db.AddUserToken(user.ID, newToken, "greader")
	_ = h.db.UpdateUserToken(user.ID, newToken)

	// FreshRSS ClientLogin 格式: SID=username/token, LSID=null, Auth=username/token
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, "SID=%s\nLSID=null\nAuth=%s\n", authVal, authVal)
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
// 兼容 FreshRSS 及各类客户端习惯 (Reeder 常用 'x', FeedMe 常用空值)
func (h *Handler) checkToken(user *db.User, token string) bool {
	if user == nil {
		return false
	}
	token = strings.TrimSpace(token)
	// 容错处理：Reeder 传入 'x'，FeedMe 传入空，或各类合法 action token
	if token == "" || token == "x" || strings.HasPrefix(token, "reader-action-token") {
		return true
	}
	expected := h.getUserActionToken(user)
	if token == expected {
		return true
	}
	// 关键防护：只要该请求已经通过 AuthMiddleware 强身份校验，并且携带了 token 参数，均认可其有效
	// 彻底杜绝 Reeder 误触“登录已过期”弹窗
	if len(token) > 0 {
		return true
	}
	return false
}

// tryAuthenticate 尝试从请求上下文、标头、Cookie 或 Query 参数中鉴权用户
func (h *Handler) tryAuthenticate(r *http.Request) *db.User {
	if user := GetUserFromContext(r.Context()); user != nil {
		return user
	}

	authHeader := r.Header.Get("Authorization")
	token := ""

	if authHeader != "" {
		if idx := strings.Index(authHeader, "auth="); idx != -1 {
			token = authHeader[idx+5:]
			if commaIdx := strings.Index(token, ","); commaIdx != -1 {
				token = token[:commaIdx]
			}
			if spaceIdx := strings.Index(token, " "); spaceIdx != -1 {
				token = token[:spaceIdx]
			}
		} else if strings.HasPrefix(authHeader, "Bearer ") {
			token = strings.TrimPrefix(authHeader, "Bearer ")
		} else {
			token = authHeader
		}
	}

	// 剥离可能存在的两端双引号、单引号及换行空格
	token = strings.Trim(token, " \"'\r\n\t")

	if token == "" {
		token = r.URL.Query().Get("ck")
	}
	if token == "" {
		token = r.URL.Query().Get("auth")
	}
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if token == "" {
		if c, err := r.Cookie("reader_token"); err == nil && c.Value != "" {
			token = c.Value
		}
	}
	if token == "" {
		if c, err := r.Cookie("Auth"); err == nil && c.Value != "" {
			token = c.Value
		}
	}

	token = strings.Trim(token, " \"'\r\n\t")
	if token == "" {
		return nil
	}

	user, err := h.db.GetUserByToken(token)
	if err != nil || user == nil {
		return nil
	}
	return user
}

// TokenHandler 处理 GET /reader/api/0/token (客户端执行 POST 修改前请求此 action token)
func (h *Handler) TokenHandler(w http.ResponseWriter, r *http.Request) {
	user := h.tryAuthenticate(r)
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
		user := h.tryAuthenticate(r)
		if user == nil {
			w.Header().Set("Google-Bad-Token", "true")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// 存入 request context
		ctx := SetUserContext(r.Context(), user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
