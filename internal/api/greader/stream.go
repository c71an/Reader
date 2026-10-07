package greader

import (
	"encoding/json"
	"fmt"
	"net/http"
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
	ID          string              `json:"id"`
	CrawlTimeMsec string            `json:"crawlTimeMsec"`
	TimestampUsec string            `json:"timestampUsec"`
	Published   int64               `json:"published"`
	Updated     int64               `json:"updated"`
	Title       string              `json:"title"`
	PublishedUsec string            `json:"publishedUsec"`
	Canonical   []StreamLink        `json:"canonical,omitempty"`
	Alternate   []StreamLink        `json:"alternate,omitempty"`
	Categories  []string            `json:"categories"`
	Origin      StreamOrigin        `json:"origin"`
	Summary     StreamContentBody   `json:"summary,omitempty"`
	Content     StreamContentBody   `json:"content,omitempty"`
	Author      string              `json:"author,omitempty"`
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
	ID            string `json:"id"`
	TimestampUsec string `json:"timestampUsec"`
}

// StreamContentsHandler 处理 GET /reader/api/0/stream/contents/*
func (h *Handler) StreamContentsHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	
	// 从 URL 路径截取 streamID
	// 如 /reader/api/0/stream/contents/user/-/state/com.google/reading-list
	path := r.URL.Path
	streamID := ""
	prefix := "/reader/api/0/stream/contents/"
	if idx := strings.Index(path, prefix); idx != -1 {
		streamID = path[idx+len(prefix):]
	}
	if streamID == "" {
		streamID = r.URL.Query().Get("s")
	}
	if streamID == "" {
		streamID = "user/-/state/com.google/reading-list"
	}

	excludeTarget := r.URL.Query().Get("xt")
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

	items, err := h.db.GetStreamItems(db.StreamQueryFilter{
		UserID:        user.ID,
		StreamID:      streamID,
		ExcludeTarget: excludeTarget,
		Limit:         limit,
		Continuation:  continuation,
		OrderOldest:   orderOldest,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	now := time.Now()
	var outItems []StreamItemOutput
	var lastID int64
	for _, it := range items {
		lastID = it.ID
		cats := []string{
			"user/-/state/com.google/reading-list",
		}
		if it.IsRead {
			cats = append(cats, "user/-/state/com.google/read")
		} else {
			cats = append(cats, "user/-/state/com.google/fresh")
		}
		if it.IsStarred {
			cats = append(cats, "user/-/state/com.google/starred")
		}

		pubUnix := it.PublishedAt.Unix()
		pubUsec := fmt.Sprintf("%d", it.PublishedAt.UnixNano()/1000)
		crawlMsec := fmt.Sprintf("%d", it.PublishedAt.UnixMilli())

		tagItemHex := fmt.Sprintf("tag:google.com,2005:reader/item/%016x", it.ID)

		outItems = append(outItems, StreamItemOutput{
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
				Title:    it.FeedTitle,
				HTMLURL:  it.URL,
			},
			Content: StreamContentBody{
				Direction: "ltr",
				Content:   it.Content,
			},
			Author: it.Author,
		})
	}

	resp := StreamContentsResponse{
		Direction:   "ltr",
		ID:          streamID,
		Title:       "Articles",
		Updated:     now.Unix(),
		UpdatedUsec: now.UnixNano() / 1000,
		Items:       outItems,
	}

	if len(items) == limit {
		resp.Continuation = fmt.Sprintf("%d", lastID)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}

// StreamItemIDsHandler 处理 GET /reader/api/0/stream/items/ids
func (h *Handler) StreamItemIDsHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	streamID := r.URL.Query().Get("s")
	if streamID == "" {
		streamID = "user/-/state/com.google/reading-list"
	}
	excludeTarget := r.URL.Query().Get("xt")
	limit := 1000

	items, err := h.db.GetStreamItems(db.StreamQueryFilter{
		UserID:        user.ID,
		StreamID:      streamID,
		ExcludeTarget: excludeTarget,
		Limit:         limit,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var refs []ItemRef
	for _, it := range items {
		refs = append(refs, ItemRef{
			ID:            fmt.Sprintf("%d", it.ID),
			TimestampUsec: fmt.Sprintf("%d", it.PublishedAt.UnixNano()/1000),
		})
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(ItemIDsResponse{
		ItemRefs: refs,
	})
}

