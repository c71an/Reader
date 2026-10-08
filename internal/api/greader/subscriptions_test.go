package greader

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reader/internal/db"
)

func setupTestDB(t *testing.T) (*db.DB, *Handler) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.InitDB(dbPath, "testuser", "testpass")
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	handler := NewHandler(database, nil)
	return database, handler
}

func TestQuickAddHandler(t *testing.T) {
	_, handler := setupTestDB(t)

	// 1. 测试添加新订阅
	formData := url.Values{
		"quickadd": {"https://example.com/rss.xml"},
		"a":        {"user/-/label/Tech"},
	}
	req := httptest.NewRequest(http.MethodPost, "/reader/api/0/subscription/quickadd", strings.NewReader(formData.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	handler.QuickAddHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("QuickAddHandler returned status %d; want 200", rec.Code)
	}

	var resp QuickAddResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.NumResults != 1 {
		t.Errorf("resp.NumResults = %d; want 1", resp.NumResults)
	}
	if !strings.HasPrefix(resp.StreamID, "feed/") {
		t.Errorf("resp.StreamID = %q; want prefix feed/", resp.StreamID)
	}

	// 2. 测试重复添加（幂等且不报错）
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/reader/api/0/subscription/quickadd", strings.NewReader(formData.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.QuickAddHandler(rec2, req2)

	var resp2 QuickAddResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("failed to decode response 2: %v", err)
	}
	if resp2.NumResults != 1 || resp2.StreamID != resp.StreamID {
		t.Errorf("duplicate quickadd mismatch: %+v vs %+v", resp2, resp)
	}
}

func TestSubscriptionEditHandler(t *testing.T) {
	_, handler := setupTestDB(t)

	// 1. Subscribe
	formSub := url.Values{
		"ac": {"subscribe"},
		"s":  {"feed/https://testnews.org/feed"},
		"t":  {"Test News"},
		"a":  {"user/-/label/News"},
	}
	reqSub := httptest.NewRequest(http.MethodPost, "/reader/api/0/subscription/edit", strings.NewReader(formSub.Encode()))
	reqSub.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recSub := httptest.NewRecorder()
	handler.SubscriptionEditHandler(recSub, reqSub)
	if recSub.Code != http.StatusOK || recSub.Body.String() != "OK" {
		t.Fatalf("subscribe failed: code=%d, body=%s", recSub.Code, recSub.Body.String())
	}

	feed, err := handler.db.GetFeedByURL("https://testnews.org/feed")
	if err != nil || feed == nil {
		t.Fatalf("feed not found in DB after subscribe: %v", err)
	}
	if feed.Title != "Test News" {
		t.Errorf("feed.Title = %q; want 'Test News'", feed.Title)
	}
	if feed.CategoryName != "News" {
		t.Errorf("feed.CategoryName = %q; want 'News'", feed.CategoryName)
	}

	// 2. Edit (修改标题和分类)
	formEdit := url.Values{
		"ac": {"edit"},
		"s":  {feed.FeedURL},
		"t":  {"Updated Title"},
		"r":  {"user/-/label/News"},
		"a":  {"user/-/label/Daily"},
	}
	reqEdit := httptest.NewRequest(http.MethodPost, "/reader/api/0/subscription/edit", strings.NewReader(formEdit.Encode()))
	reqEdit.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recEdit := httptest.NewRecorder()
	handler.SubscriptionEditHandler(recEdit, reqEdit)
	if recEdit.Code != http.StatusOK || recEdit.Body.String() != "OK" {
		t.Fatalf("edit failed: code=%d, body=%s", recEdit.Code, recEdit.Body.String())
	}

	feedUpdated, err := handler.db.GetFeedByID(feed.ID)
	if err != nil || feedUpdated == nil {
		t.Fatalf("feed not found: %v", err)
	}
	if feedUpdated.Title != "Updated Title" {
		t.Errorf("feedUpdated.Title = %q; want 'Updated Title'", feedUpdated.Title)
	}
	if feedUpdated.CategoryName != "Daily" {
		t.Errorf("feedUpdated.CategoryName = %q; want 'Daily'", feedUpdated.CategoryName)
	}

	// 3. Unsubscribe (通过 URL)
	formUnsub := url.Values{
		"ac": {"unsubscribe"},
		"s":  {"feed/" + feed.FeedURL},
	}
	reqUnsub := httptest.NewRequest(http.MethodPost, "/reader/api/0/subscription/edit", strings.NewReader(formUnsub.Encode()))
	reqUnsub.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recUnsub := httptest.NewRecorder()
	handler.SubscriptionEditHandler(recUnsub, reqUnsub)
	if recUnsub.Code != http.StatusOK || recUnsub.Body.String() != "OK" {
		t.Fatalf("unsubscribe failed: code=%d, body=%s", recUnsub.Code, recUnsub.Body.String())
	}

	feedDeleted, _ := handler.db.GetFeedByID(feed.ID)
	if feedDeleted != nil {
		t.Errorf("expected feed to be deleted, but still exists")
	}
}

func TestStreamHandlers(t *testing.T) {
	database, handler := setupTestDB(t)

	user, err := database.GetUserByUsername("testuser")
	if err != nil {
		t.Fatalf("failed to get testuser: %v", err)
	}

	feed := &db.Feed{
		Title:         "Stream Feed",
		FeedURL:       "https://stream.example.com/rss",
		ScheduleType:  "interval",
		ScheduleValue: "60m",
	}
	if err := database.CreateFeed(feed); err != nil {
		t.Fatalf("CreateFeed failed: %v", err)
	}

	articles := []*db.Article{
		{
			FeedID: feed.ID,
			GUID:   "item-1",
			Title:  "Article 1",
			URL:    "https://example.com/1",
		},
		{
			FeedID: feed.ID,
			GUID:   "item-2",
			Title:  "Article 2",
			URL:    "https://example.com/2",
		},
	}
	if _, err := database.SaveArticles(articles); err != nil {
		t.Fatalf("SaveArticles failed: %v", err)
	}

	// 1. Test StreamContentsHandler
	req := httptest.NewRequest(http.MethodGet, "/reader/api/0/stream/contents/feed/"+feed.FeedURL, nil)
	req = req.WithContext(SetUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	handler.StreamContentsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("StreamContentsHandler code = %d; want 200", rec.Code)
	}
	var streamResp StreamContentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &streamResp); err != nil {
		t.Fatalf("failed to decode StreamContentsResponse: %v", err)
	}
	if len(streamResp.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(streamResp.Items))
	}

	// 2. Test StreamItemIDsHandler
	reqIDs := httptest.NewRequest(http.MethodGet, "/reader/api/0/stream/items/ids?s=feed/"+feed.FeedURL, nil)
	reqIDs = reqIDs.WithContext(SetUserContext(reqIDs.Context(), user))
	recIDs := httptest.NewRecorder()
	handler.StreamItemIDsHandler(recIDs, reqIDs)

	if recIDs.Code != http.StatusOK {
		t.Fatalf("StreamItemIDsHandler code = %d; want 200", recIDs.Code)
	}
	var idsResp ItemIDsResponse
	if err := json.Unmarshal(recIDs.Body.Bytes(), &idsResp); err != nil {
		t.Fatalf("failed to decode ItemIDsResponse: %v", err)
	}
	if len(idsResp.ItemRefs) != 2 {
		t.Errorf("expected 2 item refs, got %d", len(idsResp.ItemRefs))
	}

	// 3. Test StreamItemsContentsHandler with item IDs
	firstID := idsResp.ItemRefs[0].ID
	reqItems := httptest.NewRequest(http.MethodGet, "/reader/api/0/stream/items/contents?i="+firstID, nil)
	reqItems = reqItems.WithContext(SetUserContext(reqItems.Context(), user))
	recItems := httptest.NewRecorder()
	handler.StreamItemsContentsHandler(recItems, reqItems)

	if recItems.Code != http.StatusOK {
		t.Fatalf("StreamItemsContentsHandler code = %d; want 200", recItems.Code)
	}
	var itemsResp StreamContentsResponse
	if err := json.Unmarshal(recItems.Body.Bytes(), &itemsResp); err != nil {
		t.Fatalf("failed to decode items contents resp: %v", err)
	}
	if len(itemsResp.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(itemsResp.Items))
	}

	// 4. Test EditTagHandler (mark read)
	formTag := url.Values{
		"i": {firstID},
		"a": {"user/-/state/com.google/read"},
	}
	reqTag := httptest.NewRequest(http.MethodPost, "/reader/api/0/edit-tag", strings.NewReader(formTag.Encode()))
	reqTag.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqTag = reqTag.WithContext(SetUserContext(reqTag.Context(), user))
	recTag := httptest.NewRecorder()
	handler.EditTagHandler(recTag, reqTag)
	if recTag.Code != http.StatusOK {
		t.Errorf("EditTagHandler code = %d; want 200", recTag.Code)
	}

	// 5. Test MarkAllAsReadHandler
	formMarkAll := url.Values{
		"s": {"feed/" + feed.FeedURL},
	}
	reqMarkAll := httptest.NewRequest(http.MethodPost, "/reader/api/0/mark-all-as-read", strings.NewReader(formMarkAll.Encode()))
	reqMarkAll.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqMarkAll = reqMarkAll.WithContext(SetUserContext(reqMarkAll.Context(), user))
	recMarkAll := httptest.NewRecorder()
	handler.MarkAllAsReadHandler(recMarkAll, reqMarkAll)
	if recMarkAll.Code != http.StatusOK {
		t.Errorf("MarkAllAsReadHandler code = %d; want 200", recMarkAll.Code)
	}
}

func TestUnauthorizedRequests(t *testing.T) {
	_, handler := setupTestDB(t)

	// Requests without user in context should safely return 401 without panicking
	req := httptest.NewRequest(http.MethodGet, "/reader/api/0/user-info", nil)
	rec := httptest.NewRecorder()
	handler.UserInfoHandler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("UserInfoHandler code = %d; want 401", rec.Code)
	}

	recUnread := httptest.NewRecorder()
	handler.UnreadCountHandler(recUnread, req)
	if recUnread.Code != http.StatusUnauthorized {
		t.Errorf("UnreadCountHandler code = %d; want 401", recUnread.Code)
	}

	recStream := httptest.NewRecorder()
	handler.StreamContentsHandler(recStream, req)
	if recStream.Code != http.StatusUnauthorized {
		t.Errorf("StreamContentsHandler code = %d; want 401", recStream.Code)
	}

	recEdit := httptest.NewRecorder()
	handler.EditTagHandler(recEdit, req)
	if recEdit.Code != http.StatusUnauthorized {
		t.Errorf("EditTagHandler code = %d; want 401", recEdit.Code)
	}
}

func TestNewArticleSyncDetails(t *testing.T) {
	_, handler := setupTestDB(t)
	user, err := handler.db.GetUserByUsername("testuser")
	if err != nil {
		t.Fatalf("failed to get user: %v", err)
	}

	cat, err := handler.db.GetOrCreateCategory("TechNews")
	if err != nil {
		t.Fatalf("failed to create category: %v", err)
	}

	feed := &db.Feed{
		Title:         "Tech Blog",
		FeedURL:       "https://tech.blog/rss",
		CategoryID:    &cat.ID,
		ScheduleType:  "interval",
		ScheduleValue: "60m",
	}
	if err := handler.db.CreateFeed(feed); err != nil {
		t.Fatalf("failed to create feed: %v", err)
	}

	// 插入测试文章
	articles := []*db.Article{
		{
			FeedID:      feed.ID,
			GUID:        "guid-101",
			Title:       "Post 1",
			URL:         "https://tech.blog/p/1",
			Content:     "Hello tech world",
			Author:      "Author A",
			PublishedAt: time.Now(),
		},
	}
	if _, err := handler.db.SaveArticles(articles); err != nil {
		t.Fatalf("failed to save articles: %v", err)
	}

	// 1. 验证 stream/items/ids 返回 directStreamIds
	reqIDs := httptest.NewRequest(http.MethodGet, "/reader/api/0/stream/items/ids?s=user/-/state/com.google/reading-list", nil)
	reqIDs = reqIDs.WithContext(SetUserContext(reqIDs.Context(), user))
	recIDs := httptest.NewRecorder()
	handler.StreamItemIDsHandler(recIDs, reqIDs)
	if recIDs.Code != http.StatusOK {
		t.Fatalf("StreamItemIDsHandler code = %d; want 200", recIDs.Code)
	}

	var idsResp ItemIDsResponse
	if err := json.Unmarshal(recIDs.Body.Bytes(), &idsResp); err != nil {
		t.Fatalf("failed to unmarshal idsResp: %v", err)
	}
	if len(idsResp.ItemRefs) == 0 {
		t.Fatalf("expected at least 1 itemRef, got 0")
	}
	firstRef := idsResp.ItemRefs[0]
	if len(firstRef.DirectStreamIds) == 0 {
		t.Fatalf("expected DirectStreamIds to be populated, got empty")
	}
	hasFeed := false
	hasCategory := false
	for _, s := range firstRef.DirectStreamIds {
		if s == fmt.Sprintf("feed/%d", feed.ID) {
			hasFeed = true
		}
		if s == "user/-/label/TechNews" {
			hasCategory = true
		}
	}
	if !hasFeed || !hasCategory {
		t.Errorf("DirectStreamIds missing feed or category: %+v", firstRef.DirectStreamIds)
	}

	// 2. 验证 stream/contents 文章 categories 包含分类标签
	reqStream := httptest.NewRequest(http.MethodGet, "/reader/api/0/stream/contents/user/-/state/com.google/reading-list", nil)
	reqStream = reqStream.WithContext(SetUserContext(reqStream.Context(), user))
	recStream := httptest.NewRecorder()
	handler.StreamContentsHandler(recStream, reqStream)
	if recStream.Code != http.StatusOK {
		t.Fatalf("StreamContentsHandler code = %d; want 200", recStream.Code)
	}

	var streamResp StreamContentsResponse
	if err := json.Unmarshal(recStream.Body.Bytes(), &streamResp); err != nil {
		t.Fatalf("failed to unmarshal streamResp: %v", err)
	}
	if len(streamResp.Items) == 0 {
		t.Fatalf("expected at least 1 item, got 0")
	}
	hasCatTag := false
	for _, c := range streamResp.Items[0].Categories {
		if c == "user/-/label/TechNews" {
			hasCatTag = true
		}
	}
	if !hasCatTag {
		t.Errorf("expected item.Categories to contain 'user/-/label/TechNews', got %+v", streamResp.Items[0].Categories)
	}

	// 3. 验证 unread-count 包含分类未读数
	reqUnread := httptest.NewRequest(http.MethodGet, "/reader/api/0/unread-count", nil)
	reqUnread = reqUnread.WithContext(SetUserContext(reqUnread.Context(), user))
	recUnread := httptest.NewRecorder()
	handler.UnreadCountHandler(recUnread, reqUnread)
	if recUnread.Code != http.StatusOK {
		t.Fatalf("UnreadCountHandler code = %d; want 200", recUnread.Code)
	}

	var unreadResp UnreadCountResponse
	if err := json.Unmarshal(recUnread.Body.Bytes(), &unreadResp); err != nil {
		t.Fatalf("failed to unmarshal unreadResp: %v", err)
	}
	hasCatUnread := false
	for _, uc := range unreadResp.UnreadCounts {
		if uc.ID == "user/-/label/TechNews" && uc.Count == 1 {
			hasCatUnread = true
		}
	}
	if !hasCatUnread {
		t.Errorf("expected unread counts to include 'user/-/label/TechNews' with count 1, got %+v", unreadResp.UnreadCounts)
	}
}

func TestMultiDeviceTokensCoexist(t *testing.T) {
	database, handler := setupTestDB(t)

	// 1. 设备 A (如 iPhone Reeder) 登录
	formLoginA := url.Values{
		"Email":  {"testuser"},
		"Passwd": {"testpass"},
	}
	reqA := httptest.NewRequest(http.MethodPost, "/accounts/ClientLogin", strings.NewReader(formLoginA.Encode()))
	reqA.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recA := httptest.NewRecorder()
	handler.ClientLogin(recA, reqA)
	if recA.Code != http.StatusOK {
		t.Fatalf("ClientLogin A failed: %d", recA.Code)
	}

	tokenA := ""
	for _, line := range strings.Split(recA.Body.String(), "\n") {
		if strings.HasPrefix(line, "Auth=") {
			tokenA = strings.TrimPrefix(line, "Auth=")
			break
		}
	}
	if tokenA == "" {
		t.Fatalf("tokenA is empty")
	}

	// 2. 模拟 Web 端登录 (生成独立的 web_session_ token)
	webToken := "web_session_test_12345"
	user, _ := database.GetUserByUsername("testuser")
	if err := database.AddUserToken(user.ID, webToken, "web"); err != nil {
		t.Fatalf("AddUserToken for web failed: %v", err)
	}

	// 3. 设备 B (如 iPad Reeder) 登录
	recB := httptest.NewRecorder()
	reqB := httptest.NewRequest(http.MethodPost, "/accounts/ClientLogin", strings.NewReader(formLoginA.Encode()))
	reqB.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ClientLogin(recB, reqB)
	if recB.Code != http.StatusOK {
		t.Fatalf("ClientLogin B failed: %d", recB.Code)
	}
	tokenB := ""
	for _, line := range strings.Split(recB.Body.String(), "\n") {
		if strings.HasPrefix(line, "Auth=") {
			tokenB = strings.TrimPrefix(line, "Auth=")
			break
		}
	}
	if tokenB == "" || tokenB == tokenA {
		t.Fatalf("tokenB should be non-empty and distinct from tokenA: %q vs %q", tokenB, tokenA)
	}

	// 4. 关键验证：设备 A 的 Token、Web 端的 Token、设备 B 的 Token 必须全部同时有效！
	for name, tok := range map[string]string{
		"Device A (iPhone)": tokenA,
		"Web Session":       webToken,
		"Device B (iPad)":   tokenB,
	} {
		u, err := database.GetUserByToken(tok)
		if err != nil || u == nil {
			t.Errorf("%s token %q invalidated unexpectedly: %v", name, tok, err)
		}
	}
}

func TestFreshRSSCompatibility_HexAndDecimalIDs(t *testing.T) {
	// 验证 "0000000000000010" 正确识别为 16 (0x10)，而不是十进制 10！
	testCases := []struct {
		input    string
		expected int64
	}{
		{"0000000000000010", 16},
		{"tag:google.com,2005:reader/item/0000000000000010", 16},
		{"tag:google.com,2005:reader/item/0000000000000020", 32},
		{"tag:google.com,2005:reader/item/000000000000000a", 10},
		{"16", 16},
		{"10", 10},
		{"12345", 12345},
	}

	for _, tc := range testCases {
		actual := parseArticleID(tc.input)
		if actual != tc.expected {
			t.Errorf("parseArticleID(%q) = %d; want %d", tc.input, actual, tc.expected)
		}
	}
}

func TestFreshRSSCompatibility_PaginationAndTimeFilter(t *testing.T) {
	database, handler := setupTestDB(t)
	user, _ := database.GetUserByUsername("testuser")

	feed := &db.Feed{
		Title:         "Pagination Feed",
		FeedURL:       "https://page.test/feed",
		ScheduleType:  "interval",
		ScheduleValue: "60m",
	}
	_ = database.CreateFeed(feed)

	now := time.Now().Truncate(time.Second)
	var articles []*db.Article
	for i := 1; i <= 5; i++ {
		articles = append(articles, &db.Article{
			FeedID:      feed.ID,
			GUID:        fmt.Sprintf("page-item-%d", i),
			Title:       fmt.Sprintf("Article %d", i),
			URL:         fmt.Sprintf("https://page.test/%d", i),
			PublishedAt: now.Add(time.Duration(i) * time.Minute),
		})
	}
	_, err := database.SaveArticles(articles)
	if err != nil {
		t.Fatalf("SaveArticles failed: %v", err)
	}

	// 1. 获取第一页 (limit=2)
	req1 := httptest.NewRequest(http.MethodGet, "/reader/api/0/stream/contents/user/-/state/com.google/reading-list?n=2", nil)
	req1 = req1.WithContext(SetUserContext(req1.Context(), user))
	rec1 := httptest.NewRecorder()
	handler.StreamContentsHandler(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("page 1 failed: %d", rec1.Code)
	}

	var resp1 StreamContentsResponse
	_ = json.Unmarshal(rec1.Body.Bytes(), &resp1)
	if len(resp1.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(resp1.Items))
	}
	if resp1.Continuation == "" {
		t.Fatalf("expected non-empty continuation token for page 1")
	}

	// 2. 获取第二页 (带 continuation)
	req2 := httptest.NewRequest(http.MethodGet, "/reader/api/0/stream/contents/user/-/state/com.google/reading-list?n=2&c="+resp1.Continuation, nil)
	req2 = req2.WithContext(SetUserContext(req2.Context(), user))
	rec2 := httptest.NewRecorder()
	handler.StreamContentsHandler(rec2, req2)

	var resp2 StreamContentsResponse
	_ = json.Unmarshal(rec2.Body.Bytes(), &resp2)
	if len(resp2.Items) != 2 {
		t.Fatalf("expected 2 items in page 2, got %d", len(resp2.Items))
	}

	// 验证两页之间文章无重复
	for _, it1 := range resp1.Items {
		for _, it2 := range resp2.Items {
			if it1.ID == it2.ID {
				t.Errorf("duplicate item found across paginated responses: %s", it1.ID)
			}
		}
	}

	// 3. 测试 ot (起始时间过滤)
	otUnix := now.Add(4 * time.Minute).Unix()
	reqOt := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/reader/api/0/stream/contents/user/-/state/com.google/reading-list?ot=%d", otUnix), nil)
	reqOt = reqOt.WithContext(SetUserContext(reqOt.Context(), user))
	recOt := httptest.NewRecorder()
	handler.StreamContentsHandler(recOt, reqOt)

	var respOt StreamContentsResponse
	_ = json.Unmarshal(recOt.Body.Bytes(), &respOt)
	if len(respOt.Items) != 2 {
		t.Errorf("expected 2 items with ot filter, got %d", len(respOt.Items))
	}
}

func TestFreshRSSCompatibility_TagManagementAndMarkCategoryRead(t *testing.T) {
	database, handler := setupTestDB(t)
	user, _ := database.GetUserByUsername("testuser")

	catTech, _ := database.GetOrCreateCategory("Technology")
	catLife, _ := database.GetOrCreateCategory("Life")

	feedTech := &db.Feed{
		Title:         "Tech Feed",
		FeedURL:       "https://tech.test/rss",
		CategoryID:    &catTech.ID,
		ScheduleType:  "interval",
		ScheduleValue: "60m",
	}
	_ = database.CreateFeed(feedTech)

	feedLife := &db.Feed{
		Title:         "Life Feed",
		FeedURL:       "https://life.test/rss",
		CategoryID:    &catLife.ID,
		ScheduleType:  "interval",
		ScheduleValue: "60m",
	}
	_ = database.CreateFeed(feedLife)

	// 分别写入 Tech 与 Life 文章
	_, _ = database.SaveArticles([]*db.Article{
		{FeedID: feedTech.ID, GUID: "tech-1", Title: "Tech 1", URL: "https://tech.test/1", PublishedAt: time.Now()},
		{FeedID: feedLife.ID, GUID: "life-1", Title: "Life 1", URL: "https://life.test/1", PublishedAt: time.Now()},
	})

	// 1. 测试 rename-tag
	formRename := url.Values{
		"s":    {"user/-/label/Technology"},
		"dest": {"user/-/label/ScienceAndTech"},
	}
	reqRename := httptest.NewRequest(http.MethodPost, "/reader/api/0/rename-tag", strings.NewReader(formRename.Encode()))
	reqRename.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqRename = reqRename.WithContext(SetUserContext(reqRename.Context(), user))
	recRename := httptest.NewRecorder()
	handler.RenameTagHandler(recRename, reqRename)
	if recRename.Code != http.StatusOK || recRename.Body.String() != "OK" {
		t.Fatalf("RenameTagHandler failed: code=%d, body=%s", recRename.Code, recRename.Body.String())
	}

	renamedCat, _ := database.GetCategoryByName("ScienceAndTech")
	if renamedCat == nil {
		t.Fatalf("expected category to be renamed to ScienceAndTech")
	}

	// 2. 测试仅标记 ScienceAndTech 分类为全部已读 (关键修复：绝不能误将 Life 也标记为已读！)
	formMarkCat := url.Values{
		"s": {"user/-/label/ScienceAndTech"},
	}
	reqMarkCat := httptest.NewRequest(http.MethodPost, "/reader/api/0/mark-all-as-read", strings.NewReader(formMarkCat.Encode()))
	reqMarkCat.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqMarkCat = reqMarkCat.WithContext(SetUserContext(reqMarkCat.Context(), user))
	recMarkCat := httptest.NewRecorder()
	handler.MarkAllAsReadHandler(recMarkCat, reqMarkCat)
	if recMarkCat.Code != http.StatusOK || recMarkCat.Body.String() != "OK" {
		t.Fatalf("MarkAllAsReadHandler for category failed: code=%d", recMarkCat.Code)
	}

	// 验证 Tech 文章已读，但 Life 文章仍然为未读！
	unreadCounts, totalUnread, err := database.GetUnreadCounts(user.ID)
	if err != nil {
		t.Fatalf("GetUnreadCounts failed: %v", err)
	}
	if totalUnread != 1 {
		t.Errorf("totalUnread = %d; want 1 (only Life article should be unread)", totalUnread)
	}
	for _, uc := range unreadCounts {
		if uc.ID == fmt.Sprintf("feed/%d", feedTech.ID) && uc.Count != 0 {
			t.Errorf("feedTech unread count = %d; want 0", uc.Count)
		}
		if uc.ID == fmt.Sprintf("feed/%d", feedLife.ID) && uc.Count != 1 {
			t.Errorf("feedLife unread count = %d; want 1", uc.Count)
		}
	}

	// 3. 测试 disable-tag (删除分类)
	formDisable := url.Values{
		"s": {"user/-/label/Life"},
	}
	reqDisable := httptest.NewRequest(http.MethodPost, "/reader/api/0/disable-tag", strings.NewReader(formDisable.Encode()))
	reqDisable.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqDisable = reqDisable.WithContext(SetUserContext(reqDisable.Context(), user))
	recDisable := httptest.NewRecorder()
	handler.DisableTagHandler(recDisable, reqDisable)
	if recDisable.Code != http.StatusOK || recDisable.Body.String() != "OK" {
		t.Fatalf("DisableTagHandler failed: code=%d", recDisable.Code)
	}

	deletedCat, _ := database.GetCategoryByName("Life")
	if deletedCat != nil {
		t.Errorf("expected Life category to be deleted")
	}

	// 4. 测试 OPML 导出
	reqExport := httptest.NewRequest(http.MethodGet, "/reader/api/0/subscription/export", nil)
	reqExport = reqExport.WithContext(SetUserContext(reqExport.Context(), user))
	recExport := httptest.NewRecorder()
	handler.SubscriptionExportHandler(recExport, reqExport)
	if recExport.Code != http.StatusOK {
		t.Fatalf("SubscriptionExportHandler failed: code=%d", recExport.Code)
	}
	if !strings.Contains(recExport.Body.String(), "<opml") || !strings.Contains(recExport.Body.String(), "ScienceAndTech") {
		t.Errorf("OPML export content mismatch: %s", recExport.Body.String())
	}
}

func TestFreshRSSCompatibility_CompatibilityCheck(t *testing.T) {
	_, handler := setupTestDB(t)
	req := httptest.NewRequest(http.MethodGet, "/check/compatibility", nil)
	rec := httptest.NewRecorder()
	handler.CompatibilityCheckHandler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated check/compatibility code = %d; want 401", rec.Code)
	}

	reqAuth := httptest.NewRequest(http.MethodGet, "/check/compatibility", nil)
	reqAuth = reqAuth.WithContext(SetUserContext(reqAuth.Context(), &db.User{ID: 1, Username: "testuser"}))
	recAuth := httptest.NewRecorder()
	handler.CompatibilityCheckHandler(recAuth, reqAuth)
	if recAuth.Code != http.StatusOK || recAuth.Body.String() != "PASS" {
		t.Errorf("check/compatibility returned %d, body %q; want 200, PASS", recAuth.Code, recAuth.Body.String())
	}
}

func TestReederArticleOpenAndAuthQuoting(t *testing.T) {
	database, handler := setupTestDB(t)

	// 1. ClientLogin 登录
	formLogin := url.Values{
		"Email":  {"testuser"},
		"Passwd": {"testpass"},
	}
	reqLogin := httptest.NewRequest(http.MethodPost, "/accounts/ClientLogin", strings.NewReader(formLogin.Encode()))
	reqLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recLogin := httptest.NewRecorder()
	handler.ClientLogin(recLogin, reqLogin)
	if recLogin.Code != http.StatusOK {
		t.Fatalf("login failed: %d", recLogin.Code)
	}

	authLine := ""
	for _, l := range strings.Split(recLogin.Body.String(), "\n") {
		if strings.HasPrefix(l, "Auth=") {
			authLine = strings.TrimPrefix(l, "Auth=")
			break
		}
	}
	if authLine == "" {
		t.Fatalf("auth line missing")
	}

	// 2. 模拟 Reeder 发送带双引号的 Authorization: GoogleLogin auth="testuser/token"
	reqUserInfo := httptest.NewRequest(http.MethodGet, "/reader/api/0/user-info", nil)
	reqUserInfo.Header.Set("Authorization", fmt.Sprintf(`GoogleLogin auth="%s"`, authLine))
	recUserInfo := httptest.NewRecorder()

	handler.AuthMiddleware(http.HandlerFunc(handler.UserInfoHandler)).ServeHTTP(recUserInfo, reqUserInfo)
	if recUserInfo.Code != http.StatusOK {
		t.Fatalf("quoted auth header failed: code=%d, body=%s", recUserInfo.Code, recUserInfo.Body.String())
	}

	var userResp UserInfoResponse
	_ = json.Unmarshal(recUserInfo.Body.Bytes(), &userResp)
	if userResp.UserID != "testuser" || userResp.UserName != "testuser" {
		t.Errorf("unexpected user-info: %+v", userResp)
	}

	// 3. 模拟 Reeder 获取 Action Token
	reqToken := httptest.NewRequest(http.MethodGet, "/reader/api/0/token", nil)
	reqToken.Header.Set("Authorization", fmt.Sprintf(`GoogleLogin auth="%s"`, authLine))
	recToken := httptest.NewRecorder()
	handler.TokenHandler(recToken, reqToken)
	if recToken.Code != http.StatusOK {
		t.Fatalf("get token failed: %d", recToken.Code)
	}
	tokenReceived := strings.TrimSpace(recToken.Body.String())
	if tokenReceived == "" {
		t.Fatalf("token is empty")
	}

	// 4. 模拟 Reeder 打开文章时触发已读标记 (POST /reader/api/0/edit-tag 带 T token)
	feed := &db.Feed{Title: "Feed", FeedURL: "https://f.test/rss"}
	_ = database.CreateFeed(feed)
	_, _ = database.SaveArticles([]*db.Article{{FeedID: feed.ID, GUID: "g1", Title: "T1", URL: "https://f.test/1", PublishedAt: time.Now()}})

	formEditTag := url.Values{
		"i": {"1"},
		"a": {"user/-/state/com.google/read"},
		"T": {tokenReceived},
	}
	reqEditTag := httptest.NewRequest(http.MethodPost, "/reader/api/0/edit-tag", strings.NewReader(formEditTag.Encode()))
	reqEditTag.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqEditTag.Header.Set("Authorization", fmt.Sprintf(`GoogleLogin auth="%s"`, authLine))
	recEditTag := httptest.NewRecorder()

	handler.AuthMiddleware(http.HandlerFunc(handler.EditTagHandler)).ServeHTTP(recEditTag, reqEditTag)
	if recEditTag.Code != http.StatusOK {
		t.Fatalf("edit-tag upon opening article failed: code=%d, body=%s, header=%+v",
			recEditTag.Code, recEditTag.Body.String(), recEditTag.Header())
	}
}





