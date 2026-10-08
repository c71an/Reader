package greader

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"reader/internal/db"
)

type StreamContentsResponse struct {
	Direction    string             `json:"direction"`
	ID           string             `json:"id"`
	Title        string             `json:"title"`
	Description  string             `json:"description,omitempty"`
	Self         []StreamLink       `json:"self,omitempty"`
	Updated      int64              `json:"updated"`
	UpdatedUsec  int64              `json:"updatedUsec"`
	Items        []StreamItemOutput `json:"items"`
	Continuation string             `json:"continuation,omitempty"`
}

type StreamLink struct {
	Href string `json:"href"`
}

type StreamItemOutput struct {
	ID            string            `json:"id"`
	CrawlTimeMsec string            `json:"crawlTimeMsec"`
	TimestampUsec string            `json:"timestampUsec"`
	Published     int64             `json:"published"`
	Updated       int64             `json:"updated"`
	Title         string            `json:"title"`
	PublishedUsec string            `json:"publishedUsec"`
	Canonical     []StreamLink      `json:"canonical,omitempty"`
	Alternate     []StreamLink      `json:"alternate,omitempty"`
	Categories    []string          `json:"categories"`
	Origin        StreamOrigin      `json:"origin"`
	Summary       StreamContentBody `json:"summary,omitempty"`
	Content       StreamContentBody `json:"content,omitempty"`
	Author        string            `json:"author,omitempty"`
}

type StreamOrigin struct {
	StreamID string `json:"streamId"`
	Title    string `json:"title"`
	HTMLURL  string `json:"htmlUrl"`
}

type StreamContentBody struct {
	Direction string `json:"direction"`
	Content   string `json:"content"`
}

type ItemIDsResponse struct {
	ItemRefs     []ItemRef `json:"itemRefs"`
	Continuation string    `json:"continuation,omitempty"`
}

type ItemRef struct {
	ID              string   `json:"id"`
	TimestampUsec   string   `json:"timestampUsec"`
	DirectStreamIds []string `json:"directStreamIds,omitempty"`
}

// StreamContentsHandler 处理 GET /reader/api/0/stream/contents/*
func (h *Handler) StreamContentsHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		w.Header().Set("Google-Bad-Token", "true")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// 从 URL 路径截取 streamID
	path := r.URL.Path
	streamID := ""
	prefix := "/stream/contents/"
	if idx := strings.Index(path, prefix); idx != -1 {
		streamID = path[idx+len(prefix):]
	}
	if streamID == "" {
		streamID = r.URL.Query().Get("s")
	}
	if streamID == "" {
		streamID = "user/-/state/com.google/reading-list"
	}
	if unescaped, err := url.PathUnescape(streamID); err == nil && unescaped != "" {
		streamID = unescaped
	}

	excludeTarget := r.URL.Query().Get("xt")
	filterTarget := r.URL.Query().Get("it")

	limitStr := r.URL.Query().Get("n")
	limit := 50
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	continuationStr := r.URL.Query().Get("c")
	var continuation int64
	if continuationStr != "" {
		continuation, _ = strconv.ParseInt(continuationStr, 10, 64)
	}

	orderOldest := r.URL.Query().Get("r") == "o"

	var startTime, stopTime int64
	if otStr := r.URL.Query().Get("ot"); otStr != "" {
		startTime, _ = strconv.ParseInt(otStr, 10, 64)
	}
	if ntStr := r.URL.Query().Get("nt"); ntStr != "" {
		stopTime, _ = strconv.ParseInt(ntStr, 10, 64)
	}

	items, err := h.db.GetStreamItems(db.StreamQueryFilter{
		UserID:        user.ID,
		StreamID:      streamID,
		ExcludeTarget: excludeTarget,
		FilterTarget:  filterTarget,
		StartTime:     startTime,
		StopTime:      stopTime,
		Limit:         limit,
		Continuation:  continuation,
		OrderOldest:   orderOldest,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	now := time.Now()
	outItems := formatStreamItems(items)

	resp := StreamContentsResponse{
		Direction:   "ltr",
		ID:          streamID,
		Title:       "Articles",
		Updated:     now.Unix(),
		UpdatedUsec: now.UnixNano() / 1000,
		Items:       outItems,
	}

	if len(items) == limit && len(items) > 0 {
		resp.Continuation = fmt.Sprintf("%d", items[len(items)-1].ID)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}

// StreamItemIDsHandler 处理 GET /reader/api/0/stream/items/ids
func (h *Handler) StreamItemIDsHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		w.Header().Set("Google-Bad-Token", "true")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	streamID := r.URL.Query().Get("s")
	if streamID == "" {
		streamID = "user/-/state/com.google/reading-list"
	}
	if unescaped, err := url.QueryUnescape(streamID); err == nil && unescaped != "" {
		streamID = unescaped
	}

	excludeTarget := r.URL.Query().Get("xt")
	filterTarget := r.URL.Query().Get("it")

	limit := 1000
	if nStr := r.URL.Query().Get("n"); nStr != "" {
		if l, err := strconv.Atoi(nStr); err == nil && l > 0 {
			limit = l
		}
	}

	continuationStr := r.URL.Query().Get("c")
	var continuation int64
	if continuationStr != "" {
		continuation, _ = strconv.ParseInt(continuationStr, 10, 64)
	}

	orderOldest := r.URL.Query().Get("r") == "o"

	var startTime, stopTime int64
	if otStr := r.URL.Query().Get("ot"); otStr != "" {
		startTime, _ = strconv.ParseInt(otStr, 10, 64)
	}
	if ntStr := r.URL.Query().Get("nt"); ntStr != "" {
		stopTime, _ = strconv.ParseInt(ntStr, 10, 64)
	}

	itemRefsRaw, err := h.db.GetStreamItemIDs(db.StreamQueryFilter{
		UserID:        user.ID,
		StreamID:      streamID,
		ExcludeTarget: excludeTarget,
		FilterTarget:  filterTarget,
		StartTime:     startTime,
		StopTime:      stopTime,
		Limit:         limit,
		Continuation:  continuation,
		OrderOldest:   orderOldest,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	refs := make([]ItemRef, 0, len(itemRefsRaw))
	for _, it := range itemRefsRaw {
		directStreams := []string{fmt.Sprintf("feed/%d", it.FeedID)}
		if it.CategoryName != "" {
			directStreams = append(directStreams, fmt.Sprintf("user/-/label/%s", it.CategoryName))
		}
		pubTime := it.PublishedAt
		if pubTime.IsZero() {
			pubTime = time.Now()
		}
		refs = append(refs, ItemRef{
			ID:              fmt.Sprintf("%d", it.ID),
			TimestampUsec:   fmt.Sprintf("%d", pubTime.UnixNano()/1000),
			DirectStreamIds: directStreams,
		})
	}

	resp := ItemIDsResponse{
		ItemRefs: refs,
	}

	if len(refs) == limit && len(refs) > 0 {
		resp.Continuation = refs[len(refs)-1].ID
	}

	// 兼容 News+ 客户端：空列表时返回 0 引用避免客户端报错
	if len(refs) == 0 && r.URL.Query().Get("client") == "newsplus" {
		resp.ItemRefs = []ItemRef{{ID: "0"}}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}

// StreamItemsContentsHandler 处理 GET 或 POST /reader/api/0/stream/items/contents
// 关键接口：客户端拿到 IDs 列表后批量拉取文章具体正文内容
func (h *Handler) StreamItemsContentsHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		w.Header().Set("Google-Bad-Token", "true")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	rawIDs := getFormValues(r, "i")

	var articleIDs []int64
	for _, rawID := range rawIDs {
		id := parseArticleID(rawID)
		if id > 0 {
			articleIDs = append(articleIDs, id)
		}
	}

	if len(articleIDs) == 0 {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(StreamContentsResponse{
			Direction: "ltr",
			ID:        "user/-/state/com.google/reading-list",
			Title:     "Articles",
			Items:     []StreamItemOutput{},
		})
		return
	}

	orderOldest := r.URL.Query().Get("r") == "o"

	items, err := h.db.GetStreamItems(db.StreamQueryFilter{
		UserID:      user.ID,
		ItemIDs:     articleIDs,
		Limit:       len(articleIDs),
		OrderOldest: orderOldest,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	now := time.Now()
	outItems := formatStreamItems(items)

	resp := StreamContentsResponse{
		Direction:   "ltr",
		ID:          "user/-/state/com.google/reading-list",
		Title:       "Articles",
		Updated:     now.Unix(),
		UpdatedUsec: now.UnixNano() / 1000,
		Items:       outItems,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}

func formatStreamItems(items []*db.StreamItem) []StreamItemOutput {
	out := make([]StreamItemOutput, 0, len(items))
	for _, it := range items {
		cats := []string{
			"user/-/state/com.google/reading-list",
			"user/-/state/org.freshrss/main",
		}
		if it.IsRead {
			cats = append(cats, "user/-/state/com.google/read")
		}
		if it.IsStarred {
			cats = append(cats, "user/-/state/com.google/starred")
		}
		if it.CategoryName != "" {
			cats = append(cats, fmt.Sprintf("user/-/label/%s", it.CategoryName))
		}

		pubTime := it.PublishedAt
		if pubTime.IsZero() {
			pubTime = time.Now()
		}
		pubUnix := pubTime.Unix()
		pubUsec := fmt.Sprintf("%d", pubTime.UnixNano()/1000)
		crawlMsec := fmt.Sprintf("%d", pubTime.UnixMilli())
		tagItemHex := fmt.Sprintf("tag:google.com,2005:reader/item/%016x", it.ID)

		contentBody := StreamContentBody{
			Direction: "ltr",
			Content:   it.Content,
		}

		originTitle := it.FeedTitle
		if originTitle == "" {
			originTitle = fmt.Sprintf("Feed %d", it.FeedID)
		}

		out = append(out, StreamItemOutput{
			ID:            tagItemHex,
			CrawlTimeMsec: crawlMsec,
			TimestampUsec: pubUsec,
			Published:     pubUnix,
			Updated:       pubUnix,
			Title:         it.Title,
			PublishedUsec: pubUsec,
			Alternate:     []StreamLink{{Href: it.URL}},
			Canonical:     []StreamLink{{Href: it.URL}},
			Categories:    cats,
			Origin: StreamOrigin{
				StreamID: fmt.Sprintf("feed/%d", it.FeedID),
				Title:    originTitle,
				HTMLURL:  it.URL,
			},
			Summary: contentBody,
			Content: contentBody,
			Author:  it.Author,
		})
	}
	return out
}

// parseArticleID 解析 Google Reader API 的文章 ID (兼容十六进制与十进制)
// 依据 FreshRSS: 若存在前导 0 或包含 a-f 则作为十六进制解析，否则作为十进制解析
func parseArticleID(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	// 剔除 "tag:google.com,2005:reader/item/" 或 URL 路径前缀
	if idx := strings.LastIndex(raw, "/"); idx != -1 {
		raw = raw[idx+1:]
	}

	isHex := false
	if len(raw) > 1 && raw[0] == '0' {
		isHex = true
	} else {
		for _, ch := range raw {
			if (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F') {
				isHex = true
				break
			}
		}
	}

	if isHex {
		if val, err := strconv.ParseInt(raw, 16, 64); err == nil {
			return val
		}
	}

	if val, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return val
	}
	if val, err := strconv.ParseInt(raw, 16, 64); err == nil {
		return val
	}
	return 0
}
