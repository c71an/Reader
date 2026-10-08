package greader

import (
	"fmt"
	"net/http"
)

// CompatibilityCheckHandler 响应客户端的环境与鉴权兼容性检测 (GET /check/compatibility)
// 兼容 FreshRSS checkCompatibility 行为
func (h *Handler) CompatibilityCheckHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		w.Header().Set("Google-Bad-Token", "true")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, "PASS")
}

