package db

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type StreamQueryFilter struct {
	UserID        int64
	StreamID      string  // 如 "feed/123", "user/-/label/CategoryName"
	ItemIDs       []int64 // 指定查询文章 ID 列表
	ExcludeTarget string  // 排除目标 (如过滤未读)
	FilterTarget  string  // 包含目标
	StartTime     int64   // 获取指定时间之后的文章
	StopTime      int64   // 获取指定时间之前的文章
	Limit         int
	Continuation  string // 复合游标字符串，格式 "{timestamp}_{id}" 或纯数字 id
	OrderOldest   bool
}

type StreamItem struct {
	ID           int64     `json:"id"`
	FeedID       int64     `json:"feed_id"`
	FeedTitle    string    `json:"feed_title"`
	FeedSiteURL  string    `json:"feed_site_url"`
	CategoryName string    `json:"category_name"`
	GUID         string    `json:"guid"`
	Title        string    `json:"title"`
	URL          string    `json:"url"`
	Content      string    `json:"content"`
	Author       string    `json:"author"`
	PublishedAt  time.Time `json:"published_at"`
	IsRead       bool      `json:"is_read"`
	IsStarred    bool      `json:"is_starred"`
}

func normalizeTimestamp(ts int64) time.Time {
	if ts <= 0 {
		return time.Now().Add(24 * time.Hour).UTC()
	}
	if ts > 1e16 {
		ts = ts / 1e9 // nanoseconds
	} else if ts > 1e12 {
		ts = ts / 1e6 // microseconds
	} else if ts > 1e10 {
		ts = ts / 1e3 // milliseconds
	}
	return time.Unix(ts, 0).UTC()
}

func (d *DB) buildStreamFilterWhere(filter StreamQueryFilter) ([]string, []interface{}) {
	whereClauses := []string{"1=1"}
	args := []interface{}{filter.UserID}

	// 1. ExcludeTarget (排除已读或特定源)
	if filter.ExcludeTarget != "" {
		switch filter.ExcludeTarget {
		case "user/-/state/com.google/read", "read":
			whereClauses = append(whereClauses, "(s.is_read IS NULL OR s.is_read = 0)")
		case "user/-/state/com.google/unread", "unread":
			whereClauses = append(whereClauses, "s.is_read = 1")
		case "user/-/state/com.google/starred", "starred":
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

	// 2. FilterTarget (仅包含已读或星标)
	if filter.FilterTarget != "" {
		switch filter.FilterTarget {
		case "user/-/state/com.google/read", "read":
			whereClauses = append(whereClauses, "s.is_read = 1")
		case "user/-/state/com.google/unread", "unread":
			whereClauses = append(whereClauses, "(s.is_read IS NULL OR s.is_read = 0)")
		case "user/-/state/com.google/starred", "starred":
			whereClauses = append(whereClauses, "s.is_starred = 1")
		}
	}

	// 3. 明确指定文章 ID 列表
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
	} else if sID == "user/-/state/com.google/starred" || sID == "starred" {
		whereClauses = append(whereClauses, "s.is_starred = 1")
	} else if sID == "user/-/state/com.google/read" || sID == "read" {
		whereClauses = append(whereClauses, "s.is_read = 1")
	} else if sID == "user/-/state/com.google/unread" || sID == "unread" {
		whereClauses = append(whereClauses, "(s.is_read IS NULL OR s.is_read = 0)")
	}

	// 5. StartTime (发布在指定时间之后的条目)
	if filter.StartTime > 0 {
		startT := normalizeTimestamp(filter.StartTime)
		whereClauses = append(whereClauses, "a.published_at >= ?")
		args = append(args, startT)
	}

	// 6. StopTime (发布在指定时间之前的条目)
	if filter.StopTime > 0 {
		stopT := normalizeTimestamp(filter.StopTime)
		whereClauses = append(whereClauses, "a.published_at <= ?")
		args = append(args, stopT)
	}

	// 7. Continuation (基于发布时间与自增 ID 联合游标的分页)
	if filter.Continuation != "" {
		var contPubTime time.Time
		var contID int64
		if strings.Contains(filter.Continuation, "_") {
			parts := strings.SplitN(filter.Continuation, "_", 2)
			if sec, err := strconv.ParseInt(parts[0], 10, 64); err == nil && sec > 0 {
				contPubTime = time.Unix(sec, 0).UTC()
			}
			if id, err := strconv.ParseInt(parts[1], 10, 64); err == nil && id > 0 {
				contID = id
			}
		} else if id, err := strconv.ParseInt(filter.Continuation, 10, 64); err == nil && id > 0 {
			contID = id
			var pubAt time.Time
			if err := d.QueryRow("SELECT published_at FROM articles WHERE id = ?", id).Scan(&pubAt); err == nil {
				contPubTime = pubAt.UTC()
			}
		}

		if !contPubTime.IsZero() && contID > 0 {
			if filter.OrderOldest {
				whereClauses = append(whereClauses, "(a.published_at > ? OR (a.published_at = ? AND a.id > ?))")
			} else {
				whereClauses = append(whereClauses, "(a.published_at < ? OR (a.published_at = ? AND a.id < ?))")
			}
			args = append(args, contPubTime, contPubTime, contID)
		} else if contID > 0 {
			if filter.OrderOldest {
				whereClauses = append(whereClauses, "a.id > ?")
			} else {
				whereClauses = append(whereClauses, "a.id < ?")
			}
			args = append(args, contID)
		}
	}

	return whereClauses, args
}

// 查询时间线文章流内容
func (d *DB) GetStreamItems(filter StreamQueryFilter) ([]*StreamItem, error) {
	if filter.Limit <= 0 {
		filter.Limit = 50
	} else if filter.Limit > 500 {
		filter.Limit = 500
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
