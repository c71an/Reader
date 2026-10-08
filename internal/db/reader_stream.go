package db

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type StreamQueryFilter struct {
	UserID        int64
	StreamID      string  // 如 "user/-/state/com.google/reading-list", "feed/123", "user/-/label/CategoryName"
	ItemIDs       []int64 // 指定查询文章 ID 列表 (用于 stream/items/contents)
	ExcludeTarget string  // 如 "user/-/state/com.google/read" (过滤未读)
	FilterTarget  string  // 如 "user/-/state/com.google/starred"
	StartTime     int64   // ot (获取指定时间之后的文章)
	StopTime      int64   // nt (获取指定时间之前的文章)
	Limit         int
	Continuation  int64   // 基于文章 ID 的分页 (按 FreshRSS 规则使用 monotonic article ID)
	OrderOldest   bool    // r == "o"
}

type StreamItem struct {
	ID           int64
	FeedID       int64
	FeedTitle    string
	FeedSiteURL  string
	CategoryName string
	GUID         string
	Title        string
	URL          string
	Content      string
	Author       string
	PublishedAt  time.Time
	IsRead       bool
	IsStarred    bool
}

type StreamItemIDRef struct {
	ID           int64
	FeedID       int64
	CategoryName string
	PublishedAt  time.Time
}

func normalizeTimestamp(ts int64) time.Time {
	if ts <= 0 {
		return time.Now().Add(24 * time.Hour)
	}
	if ts > 1e16 {
		ts = ts / 1e9 // nanoseconds
	} else if ts > 1e12 {
		ts = ts / 1e6 // microseconds
	} else if ts > 1e10 {
		ts = ts / 1e3 // milliseconds
	}
	return time.Unix(ts, 0)
}

func (d *DB) buildStreamFilterWhere(filter StreamQueryFilter) ([]string, []interface{}) {
	whereClauses := []string{"1=1"}
	args := []interface{}{filter.UserID}

	// 1. ExcludeTarget (xt: 排除目标)
	if filter.ExcludeTarget != "" {
		switch filter.ExcludeTarget {
		case "user/-/state/com.google/read":
			whereClauses = append(whereClauses, "(s.is_read IS NULL OR s.is_read = 0)")
		case "user/-/state/com.google/unread":
			whereClauses = append(whereClauses, "s.is_read = 1")
		case "user/-/state/com.google/starred":
			whereClauses = append(whereClauses, "(s.is_starred IS NULL OR s.is_starred = 0)")
		default:
			if strings.HasPrefix(filter.ExcludeTarget, "feed/") {
				feedTarget := strings.TrimPrefix(filter.ExcludeTarget, "feed/")
				if id, err := strconv.ParseInt(feedTarget, 10, 64); err == nil && id > 0 {
					whereClauses = append(whereClauses, "a.feed_id != ?")
					args = append(args, id)
				} else {
					whereClauses = append(whereClauses, "f.feed_url != ?")
					args = append(args, feedTarget)
				}
			}
		}
	}

	// 2. FilterTarget (it: 包含目标)
	if filter.FilterTarget != "" {
		switch filter.FilterTarget {
		case "user/-/state/com.google/read":
			whereClauses = append(whereClauses, "s.is_read = 1")
		case "user/-/state/com.google/unread":
			whereClauses = append(whereClauses, "(s.is_read IS NULL OR s.is_read = 0)")
		case "user/-/state/com.google/starred":
			whereClauses = append(whereClauses, "s.is_starred = 1")
		}
	}

	// 3. 明确指定文章 ID 列表 (stream/items/contents 批量获取)
	if len(filter.ItemIDs) > 0 {
		placeholders := make([]string, len(filter.ItemIDs))
		for i, id := range filter.ItemIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		whereClauses = append(whereClauses, fmt.Sprintf("a.id IN (%s)", strings.Join(placeholders, ",")))
	}

	// 4. StreamID 目标
	sID := strings.TrimSpace(filter.StreamID)
	if strings.HasPrefix(sID, "feed/") {
		feedTarget := strings.TrimPrefix(sID, "feed/")
		if id, err := strconv.ParseInt(feedTarget, 10, 64); err == nil && id > 0 {
			whereClauses = append(whereClauses, "a.feed_id = ?")
			args = append(args, id)
		} else {
			whereClauses = append(whereClauses, "f.feed_url = ?")
			args = append(args, feedTarget)
		}
	} else if idx := strings.Index(sID, "/label/"); idx != -1 {
		categoryName := strings.TrimSpace(sID[idx+len("/label/"):])
		whereClauses = append(whereClauses, "c.name = ?")
		args = append(args, categoryName)
	} else if sID == "user/-/state/com.google/starred" {
		whereClauses = append(whereClauses, "s.is_starred = 1")
	} else if sID == "user/-/state/com.google/read" {
		whereClauses = append(whereClauses, "s.is_read = 1")
	} else if sID == "user/-/state/com.google/unread" {
		whereClauses = append(whereClauses, "(s.is_read IS NULL OR s.is_read = 0)")
	}

	// 5. StartTime (ot: 爬取/发布在指定时间之后的条目)
	if filter.StartTime > 0 {
		startT := normalizeTimestamp(filter.StartTime)
		whereClauses = append(whereClauses, "a.published_at >= ?")
		args = append(args, startT)
	}

	// 6. StopTime (nt: 爬取/发布在指定时间之前的条目)
	if filter.StopTime > 0 {
		stopT := normalizeTimestamp(filter.StopTime)
		whereClauses = append(whereClauses, "a.published_at <= ?")
		args = append(args, stopT)
	}

	// 7. Continuation (基于自增 ID 进行稳定无遗漏的分页)
	if filter.Continuation > 0 {
		if filter.OrderOldest {
			whereClauses = append(whereClauses, "a.id > ?")
		} else {
			whereClauses = append(whereClauses, "a.id < ?")
		}
		args = append(args, filter.Continuation)
	}

	return whereClauses, args
}

// 查询文章流内容 (用于 Google Reader API stream/contents)
func (d *DB) GetStreamItems(filter StreamQueryFilter) ([]*StreamItem, error) {
	if filter.Limit <= 0 || filter.Limit > 10000 {
		filter.Limit = 50
	}

	whereClauses, args := d.buildStreamFilterWhere(filter)

	orderDir := "DESC"
	if filter.OrderOldest {
		orderDir = "ASC"
	}

	query := fmt.Sprintf(`
		SELECT a.id, a.feed_id, f.title, COALESCE(f.site_url, ''), COALESCE(c.name, ''), a.guid, a.title, a.url, a.content, a.author, a.published_at,
		       COALESCE(s.is_read, 0) as is_read, COALESCE(s.is_starred, 0) as is_starred
		FROM articles a
		JOIN feeds f ON a.feed_id = f.id
		LEFT JOIN categories c ON f.category_id = c.id
		LEFT JOIN article_states s ON a.id = s.article_id AND s.user_id = ?
		WHERE %s
		ORDER BY a.id %s
		LIMIT %d
	`, strings.Join(whereClauses, " AND "), orderDir, filter.Limit)

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*StreamItem
	for rows.Next() {
		var item StreamItem
		var isReadInt, isStarredInt int
		if err := rows.Scan(&item.ID, &item.FeedID, &item.FeedTitle, &item.FeedSiteURL, &item.CategoryName, &item.GUID, &item.Title, &item.URL,
			&item.Content, &item.Author, &item.PublishedAt, &isReadInt, &isStarredInt); err != nil {
			return nil, err
		}
		item.IsRead = (isReadInt == 1)
		item.IsStarred = (isStarredInt == 1)
		items = append(items, &item)
	}
	return items, nil
}

// 查询轻量文章 ID 引用列表 (用于 GET /reader/api/0/stream/items/ids)
func (d *DB) GetStreamItemIDs(filter StreamQueryFilter) ([]*StreamItemIDRef, error) {
	if filter.Limit <= 0 || filter.Limit > 10000 {
		filter.Limit = 1000
	}

	whereClauses, args := d.buildStreamFilterWhere(filter)

	orderDir := "DESC"
	if filter.OrderOldest {
		orderDir = "ASC"
	}

	query := fmt.Sprintf(`
		SELECT a.id, a.feed_id, COALESCE(c.name, ''), a.published_at
		FROM articles a
		JOIN feeds f ON a.feed_id = f.id
		LEFT JOIN categories c ON f.category_id = c.id
		LEFT JOIN article_states s ON a.id = s.article_id AND s.user_id = ?
		WHERE %s
		ORDER BY a.id %s
		LIMIT %d
	`, strings.Join(whereClauses, " AND "), orderDir, filter.Limit)

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*StreamItemIDRef
	for rows.Next() {
		var ref StreamItemIDRef
		if err := rows.Scan(&ref.ID, &ref.FeedID, &ref.CategoryName, &ref.PublishedAt); err != nil {
			return nil, err
		}
		list = append(list, &ref)
	}
	return list, nil
}

// 标记单篇文章已读/未读
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

// 批量标记文章已读/未读 (事务优化性能)
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

// 标记单篇文章星标状态
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

// 批量标记文章星标状态 (事务优化性能)
func (d *DB) SetArticlesStarred(userID int64, articleIDs []int64, isStarred bool) error {
	if len(articleIDs) == 0 {
		return nil
	}
	starredInt := 0
	if isStarred {
		starredInt = 1
	}

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO article_states (user_id, article_id, is_starred)
		VALUES (?, ?, ?)
		ON CONFLICT(user_id, article_id) DO UPDATE SET
			is_starred = excluded.is_starred
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, aid := range articleIDs {
		if _, err := stmt.Exec(userID, aid, starredInt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// 标记指定 Feed 的所有文章为已读
func (d *DB) MarkFeedAllRead(userID, feedID int64, beforeTimestamp int64) error {
	beforeTime := normalizeTimestamp(beforeTimestamp)

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

// 标记指定分类下的所有文章为已读
func (d *DB) MarkCategoryAllRead(userID, categoryID int64, beforeTimestamp int64) error {
	beforeTime := normalizeTimestamp(beforeTimestamp)

	_, err := d.Exec(`
		INSERT INTO article_states (user_id, article_id, is_read, read_at)
		SELECT ?, a.id, 1, CURRENT_TIMESTAMP
		FROM articles a
		JOIN feeds f ON a.feed_id = f.id
		WHERE f.category_id = ? AND a.published_at <= ?
		ON CONFLICT(user_id, article_id) DO UPDATE SET
			is_read = 1,
			read_at = CURRENT_TIMESTAMP
	`, userID, categoryID, beforeTime)
	return err
}

// 标记所有已星标文章为已读
func (d *DB) MarkStarredAllRead(userID int64, beforeTimestamp int64) error {
	beforeTime := normalizeTimestamp(beforeTimestamp)

	_, err := d.Exec(`
		UPDATE article_states SET is_read = 1, read_at = CURRENT_TIMESTAMP
		WHERE user_id = ? AND is_starred = 1 AND article_id IN (
			SELECT id FROM articles WHERE published_at <= ?
		)
	`, userID, beforeTime)
	return err
}

// 标记全部文章为已读
func (d *DB) MarkAllArticlesRead(userID int64, beforeTimestamp int64) error {
	beforeTime := normalizeTimestamp(beforeTimestamp)

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
	ID                      string `json:"id"`
	Count                   int    `json:"count"`
	NewestItemTimestampUsec string `json:"newestItemTimestampUsec"`
}

// 获取未读总数与各个 Feed 及分类的未读统计
func (d *DB) GetUnreadCounts(userID int64) ([]UnreadCountItem, int, error) {
	// 1. 各个 Feed 的未读统计以及最新条目时间戳
	feedRows, err := d.Query(`
		SELECT f.id,
		       COUNT(CASE WHEN s.is_read IS NULL OR s.is_read = 0 THEN a.id END) as unread_count,
		       COALESCE(MAX(a.published_at), '') as newest_time
		FROM feeds f
		LEFT JOIN articles a ON a.feed_id = f.id
		LEFT JOIN article_states s ON s.article_id = a.id AND s.user_id = ?
		GROUP BY f.id
	`, userID)
	if err != nil {
		return nil, 0, err
	}
	defer feedRows.Close()

	var result []UnreadCountItem
	total := 0
	var maxTime time.Time

	for feedRows.Next() {
		var feedID int64
		var count int
		var newestTimeStr string
		if err := feedRows.Scan(&feedID, &count, &newestTimeStr); err != nil {
			return nil, 0, err
		}

		usecStr := "0"
		if newestTimeStr != "" {
			if t, err := time.Parse(time.RFC3339, newestTimeStr); err == nil {
				usecStr = fmt.Sprintf("%d", t.UnixNano()/1000)
				if t.After(maxTime) {
					maxTime = t
				}
			} else if t, err := time.Parse("2006-01-02 15:04:05", newestTimeStr); err == nil {
				usecStr = fmt.Sprintf("%d", t.UnixNano()/1000)
				if t.After(maxTime) {
					maxTime = t
				}
			}
		}

		result = append(result, UnreadCountItem{
			ID:                      fmt.Sprintf("feed/%d", feedID),
			Count:                   count,
			NewestItemTimestampUsec: usecStr,
		})
		total += count
	}

	// 2. 各个分类下的未读统计
	catRows, err := d.Query(`
		SELECT c.name,
		       COUNT(CASE WHEN s.is_read IS NULL OR s.is_read = 0 THEN a.id END) as unread_count,
		       COALESCE(MAX(a.published_at), '') as newest_time
		FROM categories c
		JOIN feeds f ON f.category_id = c.id
		LEFT JOIN articles a ON a.feed_id = f.id
		LEFT JOIN article_states s ON s.article_id = a.id AND s.user_id = ?
		GROUP BY c.name
	`, userID)
	if err == nil {
		defer catRows.Close()
		for catRows.Next() {
			var catName string
			var catCount int
			var catTimeStr string
			if err := catRows.Scan(&catName, &catCount, &catTimeStr); err == nil && catName != "" {
				usecStr := "0"
				if catTimeStr != "" {
					if t, err := time.Parse(time.RFC3339, catTimeStr); err == nil {
						usecStr = fmt.Sprintf("%d", t.UnixNano()/1000)
					} else if t, err := time.Parse("2006-01-02 15:04:05", catTimeStr); err == nil {
						usecStr = fmt.Sprintf("%d", t.UnixNano()/1000)
					}
				}
				result = append(result, UnreadCountItem{
					ID:                      fmt.Sprintf("user/-/label/%s", catName),
					Count:                   catCount,
					NewestItemTimestampUsec: usecStr,
				})
			}
		}
	}

	// 3. 全局阅读列表未读总数
	maxUsecStr := "0"
	if !maxTime.IsZero() {
		maxUsecStr = fmt.Sprintf("%d", maxTime.UnixNano()/1000)
	} else {
		maxUsecStr = fmt.Sprintf("%d", time.Now().UnixNano()/1000)
	}

	result = append(result, UnreadCountItem{
		ID:                      "user/-/state/com.google/reading-list",
		Count:                   total,
		NewestItemTimestampUsec: maxUsecStr,
	})

	return result, total, nil
}
