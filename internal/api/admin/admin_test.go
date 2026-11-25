package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"reader/internal/db"
)

type mockStore struct {
	users      map[string]*db.User
	feeds      map[int64]*db.Feed
	nextFeedID int64
	deletedIDs []int64
}

func newMockStore() *mockStore {
	return &mockStore{
		users:      make(map[string]*db.User),
		feeds:      make(map[int64]*db.Feed),
		nextFeedID: 1,
	}
}

func (m *mockStore) GetUserByUsername(username string) (*db.User, error) {
	return nil, errors.New("not found")
}
func (m *mockStore) GetUserByToken(token string) (*db.User, error) {
	if u, ok := m.users[token]; ok {
		return u, nil
	}
	return nil, errors.New("not found")
}
func (m *mockStore) AddUserToken(userID int64, token, clientType string) error { return nil }
func (m *mockStore) GetUserTokens(userID int64, currentToken string) ([]db.UserTokenInfo, error) {
	return nil, nil
}
func (m *mockStore) RevokeOtherUserTokens(userID int64, currentToken string) error { return nil }
func (m *mockStore) DeleteUserTokenByID(userID, tokenID int64) error               { return nil }
func (m *mockStore) DeleteUserToken(token string) error                            { return nil }

func (m *mockStore) GetAllFeeds() ([]*db.Feed, error) {
	out := make([]*db.Feed, 0, len(m.feeds))
	for _, f := range m.feeds {
		out = append(out, f)
	}
	return out, nil
}
func (m *mockStore) GetFeedByID(feedID int64) (*db.Feed, error) {
	if f, ok := m.feeds[feedID]; ok {
		cp := *f
		return &cp, nil
	}
	return nil, errors.New("not found")
}
func (m *mockStore) CreateFeed(feed *db.Feed) error {
	feed.ID = m.nextFeedID
	m.nextFeedID++
	cp := *feed
	m.feeds[feed.ID] = &cp
	return nil
}
func (m *mockStore) UpdateFeed(feed *db.Feed) error {
	cp := *feed
	m.feeds[feed.ID] = &cp
	return nil
}
func (m *mockStore) DeleteFeed(feedID int64) error {
	delete(m.feeds, feedID)
	m.deletedIDs = append(m.deletedIDs, feedID)
	return nil
}
func (m *mockStore) TrimFeedArticles(feedID int64, maxArticles int) (int64, error) { return 0, nil }
func (m *mockStore) GetAllCategories() ([]db.Category, error)                      { return nil, nil }
func (m *mockStore) GetOrCreateCategory(name string) (*db.Category, error) {
	return &db.Category{ID: 1, Name: name}, nil
}
func (m *mockStore) GetCategoryByName(name string) (*db.Category, error) { return nil, nil }
func (m *mockStore) UpdateCategoryByName(currentName, newName string, sortOrder int) error {
	return nil
}
func (m *mockStore) GetFeedArticles(feedID int64, userID int64, limit, offset int) ([]*db.Article, int, error) {
	return nil, 0, nil
}
func (m *mockStore) GetStreamItems(filter db.StreamQueryFilter) ([]*db.StreamItem, error) {
	return nil, nil
}
func (m *mockStore) SetArticlesRead(userID int64, articleIDs []int64, isRead bool) error { return nil }
func (m *mockStore) SetArticlesStarred(userID int64, articleIDs []int64, isStarred bool) error {
	return nil
}
func (m *mockStore) MarkFeedAllRead(userID, feedID int64, beforeTimestamp int64) error { return nil }
func (m *mockStore) MarkCategoryAllRead(userID, categoryID int64, beforeTimestamp int64) error {
	return nil
}
func (m *mockStore) MarkAllArticlesRead(userID int64, beforeTimestamp int64) error { return nil }

type mockScheduler struct {
	fetchedIDs []int64
}

func (m *mockScheduler) FetchNow(feedID int64) (int, error) {
	m.fetchedIDs = append(m.fetchedIDs, feedID)
	return 3, nil
}

func TestAuthMiddleware(t *testing.T) {
	store := newMockStore()
	store.users["valid-token"] = &db.User{ID: 1, Username: "admin"}
	h := NewAdminHandler(store, &mockScheduler{})

	protected := h.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := getUserFromContext(r.Context())
		if u == nil || u.Username != "admin" {
			t.Errorf("expected admin user in context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	// 1. 无 Token -> 401
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/profile", nil)
	protected.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}

	// 2. Bearer Token -> 200
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/admin/profile", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	protected.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid bearer token, got %d", rec.Code)
	}
}

func TestCreateFeedValidation(t *testing.T) {
	store := newMockStore()
	h := NewAdminHandler(store, &mockScheduler{})

	// 1. 缺少 feed_url -> 400
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/feeds", bytes.NewBufferString(`{"title":"Test"}`))
	h.CreateFeed(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty feed_url, got %d", rec.Code)
	}

	// 2. 非法调度表达式 -> 400
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/feeds", bytes.NewBufferString(`{"feed_url":"https://example.com/rss","schedule_value":"* 8 *"}`))
	h.CreateFeed(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid schedule, got %d", rec.Code)
	}

	// 3. 合法请求 -> 200 并使用默认调度
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/feeds", bytes.NewBufferString(`{"title":"Tech","feed_url":"https://example.com/rss"}`))
	h.CreateFeed(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid create feed, got %d", rec.Code)
	}
	var created db.Feed
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if created.ScheduleType != "cron" || created.ScheduleValue != "0 8 * ~30m" {
		t.Errorf("unexpected default schedule: %s / %s", created.ScheduleType, created.ScheduleValue)
	}
}

func TestDistributeScheduleAtomicValidation(t *testing.T) {
	store := newMockStore()
	store.feeds[1] = &db.Feed{ID: 1, Title: "F1", ScheduleType: "paused", ScheduleValue: "0 8 *"}
	store.feeds[2] = &db.Feed{ID: 2, Title: "F2", ScheduleType: "paused", ScheduleValue: "0 8 *"}
	h := NewAdminHandler(store, &mockScheduler{})

	// 非法星期表达式应整体拒绝，不能修改已有订阅
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/feeds/distribute-schedule",
		bytes.NewBufferString(`{"feed_ids":[1,2],"start_time":"08:00","interval_minutes":15,"week":"bad-week"}`))
	h.DistributeSchedule(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid week, got %d", rec.Code)
	}
	if store.feeds[1].ScheduleType != "paused" {
		t.Fatalf("feed 1 should not be mutated on invalid schedule")
	}

	// 合法错峰分配
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/feeds/distribute-schedule",
		bytes.NewBufferString(`{"feed_ids":[1,2],"start_time":"09:00","interval_minutes":30,"week":"*","jitter":"10m"}`))
	h.DistributeSchedule(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid distribute-schedule, got %d", rec.Code)
	}
	if store.feeds[1].ScheduleValue != "0 9 * ~10m" || store.feeds[2].ScheduleValue != "30 9 * ~10m" {
		t.Errorf("unexpected distributed schedules: %q, %q", store.feeds[1].ScheduleValue, store.feeds[2].ScheduleValue)
	}
}

func TestBatchAction(t *testing.T) {
	store := newMockStore()
	store.feeds[1] = &db.Feed{ID: 1, ScheduleType: "cron", ScheduleValue: "0 8 *"}
	sched := &mockScheduler{}
	h := NewAdminHandler(store, sched)

	// fetch 通过 ScheduleTrigger 接口调用
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/feeds/batch",
		bytes.NewBufferString(`{"action":"fetch","feed_ids":[1]}`))
	h.BatchAction(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if len(sched.fetchedIDs) != 1 || sched.fetchedIDs[0] != 1 {
		t.Errorf("expected FetchNow(1) to be called, got %v", sched.fetchedIDs)
	}
}
