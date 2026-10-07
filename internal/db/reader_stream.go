package db

import (
	"fmt"
	"strings"
	"time"
)

type StreamQueryFilter struct {
	UserID        int64
	StreamID      string // 如 "user/-/state/com.google/reading-list", "feed/123", "user/-/label/CategoryName"
	ItemIDs       []int64 // 指定查询文章 ID 列表 (用于 stream/items/contents)
	ExcludeTarget string // 如 "user/-/state/com.google/read" (过滤未读)
	Limit         int
	Continuation  int64  // 基于文章 ID 或时间戳的分页
	OnlyIDs       bool
	OrderOldest   bool
}

type StreamItem struct {
	ID          int64
	FeedID      int64
	FeedTitle   string
	GUID        string
	Title       string
	URL         string
	Content     string
	Author      string
	PublishedAt time.Time
	IsRead      bool
	IsStarred   bool
}

// 查询文章流 (用于 Google Reader API stream/contents)
func (d *DB) GetStreamItems(filter StreamQueryFilter) ([]*StreamItem, error) {
	if filter.Limit <= 0 || filter.Limit > 1000 {
		filter.Limit = 100
	}

	whereClauses := []string{"1=1"}
	args := []interface{}{filter.UserID}

	// 针对排除已读 (只看未读)
	if filter.ExcludeTarget == "user/-/state/com.google/read" {
		whereClauses = append(whereClauses, "(s.is_read IS NULL OR s.is_read = 0)")
	}

	// 针对明确指定的文章 ID 列表 (Reeder 的 stream/items/contents 关键接口)
	if len(filter.ItemIDs) > 0 {
		placeholders := make([]string, len(filter.ItemIDs))
		for i, id := range filter.ItemIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		whereClauses = append(whereClauses, fmt.Sprintf("a.id IN (%s)", strings.Join(placeholders, ",")))
	}

	// 针对特定目标流
	if strings.HasPrefix(filter.StreamID, "feed/") {
		feedID := strings.TrimPrefix(filter.StreamID, "feed/")
		whereClauses = append(whereClauses, "a.feed_id = ?")
		args = append(args, feedID)
	} else if strings.HasPrefix(filter.StreamID, "user/-/label/") {
		categoryName := strings.TrimPrefix(filter.StreamID, "user/-/label/")
		whereClauses = append(whereClauses, "c.name = ?")
		args = append(args, categoryName)
	} else if filter.StreamID == "user/-/state/com.google/starred" {
		whereClauses = append(whereClauses, "s.is_starred = 1")
	} else if filter.StreamID == "user/-/state/com.google/read" {
		whereClauses = append(whereClauses, "s.is_read = 1")
	}

	if filter.Continuation > 0 {
		if filter.OrderOldest {
			whereClauses = append(whereClauses, "a.id > ?")
		} else {
			whereClauses = append(whereClauses, "a.id < ?")
		}
		args = append(args, filter.Continuation)
	}

	orderDir := "DESC"
	if filter.OrderOldest {
		orderDir = "ASC"
	}

	query := fmt.Sprintf(`
		SELECT a.id, a.feed_id, f.title, a.guid, a.title, a.url, a.content, a.author, a.published_at,
		       COALESCE(s.is_read, 0) as is_read, COALESCE(s.is_starred, 0) as is_starred
		FROM articles a
		JOIN feeds f ON a.feed_id = f.id
		LEFT JOIN categories c ON f.category_id = c.id
		LEFT JOIN article_states s ON a.id = s.article_id AND s.user_id = ?
		WHERE %s
		ORDER BY a.published_at %s, a.id %s
		LIMIT %d
	`, strings.Join(whereClauses, " AND "), orderDir, orderDir, filter.Limit)

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*StreamItem
	for rows.Next() {
		var item StreamItem
		var isReadInt, isStarredInt int
		if err := rows.Scan(&item.ID, &item.FeedID, &item.FeedTitle, &item.GUID, &item.Title, &item.URL,
			&item.Content, &item.Author, &item.PublishedAt, &isReadInt, &isStarredInt); err != nil {
			return nil, err
		}
		item.IsRead = (isReadInt == 1)
		item.IsStarred = (isStarredInt == 1)
		items = append(items, &item)
	}
	return items, nil
}

// 标记文章状态（已读、标星等）
func (d *DB) MarkArticleRead(userID, articleID int64, read bool) error {
	readInt := 0
	var readAt *time.Time
	if read {
		readInt = 1
		now := time.Now()
		readAt = &now
	}

	_, err := d.Exec(`
		INSERT INTO article_states (user_id, article_id, is_read, read_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, article_id) DO UPDATE SET
			is_read = excluded.is_read,
			read_at = excluded.read_at
	`, userID, articleID, readInt, readAt)
	return err
}

func (d *DB) SetArticlesRead(userID int64, articleIDs []int64, isRead bool) error {
	if len(articleIDs) == 0 {
		return nil
	}
	readInt := 0
	var readAt *time.Time
	if isRead {
		readInt = 1
		now := time.Now()
		readAt = &now
	}

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO article_states (user_id, article_id, is_read, read_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, article_id) DO UPDATE SET
			is_read = excluded.is_read,
			read_at = excluded.read_at
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, aid := range articleIDs {
		if _, err := stmt.Exec(userID, aid, readInt, readAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) MarkArticleStarred(userID, articleID int64, starred bool) error {
	starredInt := 0
	if starred {
		starredInt = 1
	}

	_, err := d.Exec(`
		INSERT INTO article_states (user_id, article_id, is_starred)
		VALUES (?, ?, ?)
		ON CONFLICT(user_id, article_id) DO UPDATE SET
			is_starred = excluded.is_starred
	`, userID, articleID, starredInt)
	return err
}

func (d *DB) MarkFeedAllRead(userID, feedID int64, beforeTimestamp int64) error {
	beforeTime := time.Unix(beforeTimestamp, 0)
	if beforeTimestamp == 0 {
		beforeTime = time.Now()
	}

	_, err := d.Exec(`
		INSERT INTO article_states (user_id, article_id, is_read, read_at)
		SELECT ?, id, 1, CURRENT_TIMESTAMP
		FROM articles
		WHERE feed_id = ? AND published_at <= ?
		ON CONFLICT(user_id, article_id) DO UPDATE SET
			is_read = 1,
			read_at = CURRENT_TIMESTAMP
	`, userID, feedID, beforeTime)
	return err
}

func (d *DB) MarkAllArticlesRead(userID int64, beforeTimestamp int64) error {
	beforeTime := time.Unix(beforeTimestamp, 0)
	if beforeTimestamp == 0 {
		beforeTime = time.Now()
	}

	_, err := d.Exec(`
		INSERT INTO article_states (user_id, article_id, is_read, read_at)
		SELECT ?, id, 1, CURRENT_TIMESTAMP
		FROM articles
		WHERE published_at <= ?
		ON CONFLICT(user_id, article_id) DO UPDATE SET
			is_read = 1,
			read_at = CURRENT_TIMESTAMP
	`, userID, beforeTime)
	return err
}

type UnreadCountItem struct {
	ID        string    `json:"id"`
	Count     int       `json:"count"`
	NewestItemTimestampUsec int64 `json:"newestItemTimestampUsec"`
}

// 获取未读总数与各个 Feed 的未读统计
func (d *DB) GetUnreadCounts(userID int64) ([]UnreadCountItem, int, error) {
	rows, err := d.Query(`
		SELECT f.id, COUNT(a.id)
		FROM feeds f
		JOIN articles a ON a.feed_id = f.id
		LEFT JOIN article_states s ON s.article_id = a.id AND s.user_id = ?
		WHERE s.is_read IS NULL OR s.is_read = 0
		GROUP BY f.id
	`, userID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var result []UnreadCountItem
	total := 0
	for rows.Next() {
		var feedID int64
		var count int
		if err := rows.Scan(&feedID, &count); err != nil {
			return nil, 0, err
		}
		result = append(result, UnreadCountItem{
			ID:    fmt.Sprintf("feed/%d", feedID),
			Count: count,
		})
		total += count
	}

	// 加上全部未读的 stream 统计
	result = append(result, UnreadCountItem{
		ID:    "user/-/state/com.google/reading-list",
		Count: total,
	})

	return result, total, nil
}
