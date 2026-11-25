package admin

import "reader/internal/db"

// Store 定义管理后台所需的持久化契约
type Store interface {
	GetUserByUsername(username string) (*db.User, error)
	GetUserByToken(token string) (*db.User, error)
	AddUserToken(userID int64, token, clientType string) error
	GetUserTokens(userID int64, currentToken string) ([]db.UserTokenInfo, error)
	RevokeOtherUserTokens(userID int64, currentToken string) error
	DeleteUserTokenByID(userID, tokenID int64) error
	DeleteUserToken(token string) error

	GetAllFeeds() ([]*db.Feed, error)
	GetFeedByID(feedID int64) (*db.Feed, error)
	CreateFeed(feed *db.Feed) error
	UpdateFeed(feed *db.Feed) error
	DeleteFeed(feedID int64) error
	TrimFeedArticles(feedID int64, maxArticles int) (int64, error)

	GetAllCategories() ([]db.Category, error)
	GetOrCreateCategory(name string) (*db.Category, error)
	GetCategoryByName(name string) (*db.Category, error)
	UpdateCategoryByName(currentName, newName string, sortOrder int) error

	GetFeedArticles(feedID int64, userID int64, limit, offset int) ([]*db.Article, int, error)
	GetStreamItems(filter db.StreamQueryFilter) ([]*db.StreamItem, error)
	SetArticlesRead(userID int64, articleIDs []int64, isRead bool) error
	SetArticlesStarred(userID int64, articleIDs []int64, isStarred bool) error
	MarkFeedAllRead(userID, feedID int64, beforeTimestamp int64) error
	MarkCategoryAllRead(userID, categoryID int64, beforeTimestamp int64) error
	MarkAllArticlesRead(userID int64, beforeTimestamp int64) error
}

// ScheduleTrigger 定义触发立即抓取的最小调度契约
type ScheduleTrigger interface {
	FetchNow(feedID int64) (int, error)
}

type AdminHandler struct {
	db        Store
	scheduler ScheduleTrigger
}

func NewAdminHandler(store Store, sched ScheduleTrigger) *AdminHandler {
	return &AdminHandler{
		db:        store,
		scheduler: sched,
	}
}
