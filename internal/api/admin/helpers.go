package admin

import (
	"encoding/json"
	"net/http"
	"strconv"

	"reader/internal/db"

	"github.com/go-chi/chi/v5"
)

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst interface{}, errMsg string) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		if errMsg == "" {
			errMsg = err.Error()
		}
		http.Error(w, errMsg, http.StatusBadRequest)
		return false
	}
	return true
}

func requireUser(w http.ResponseWriter, r *http.Request) (*db.User, bool) {
	user := getUserFromContext(r.Context())
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return nil, false
	}
	return user, true
}

func parseIDParam(r *http.Request, param string) (int64, error) {
	return strconv.ParseInt(chi.URLParam(r, param), 10, 64)
}

func currentSessionToken(r *http.Request) string {
	if cookie, err := r.Cookie("reader_token"); err == nil && cookie != nil {
		return cookie.Value
	}
	return ""
}
