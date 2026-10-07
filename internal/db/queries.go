package db

import (
	"database/sql"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// 订阅源 CRUD
func (d *DB) CreateFeed(feed *Feed) error {
	res, err := d.Exec(`
		INSERT INTO feeds (title, feed_url, site_url, category_id, schedule_type, schedule_value)
		VALUES (?, ?, ?, ?, ?, ?)
	`, feed.Title, feed.FeedURL, feed.SiteURL, feed.CategoryID, feed.ScheduleType, feed.ScheduleValue)
	if err != nil {
		return err
	}
	feed.ID, err = res.LastInsertId()
	return err
}

func (d *DB) UpdateFeed(feed *Feed) error {
	_, err := d.Exec(`
		UPDATE feeds SET title = ?, feed_url = ?, site_url = ?, category_id = ?, schedule_type = ?, schedule_value = ?
		WHERE id = ?
	`, feed.Title, feed.FeedURL, feed.SiteURL, feed.CategoryID, feed.ScheduleType, feed.ScheduleValue, feed.ID)
	return err
}

func (d *DB) DeleteFeed(feedID int64) error {
	_, err := d.Exec("DELETE FROM feeds WHERE id = ?", feedID)
	return err
}

func (d *DB) GetFeedByID(feedID int64) (*Feed, error) {
	row := d.QueryRow(`
		SELECT f.id, f.title, f.feed_url, f.site_url, f.category_id, COALESCE(c.name, ''),
		       f.schedule_type, f.schedule_value, f.last_fetched_at, f.last_error, f.etag, f.last_modified, f.created_at
		FROM feeds f
		LEFT JOIN categories c ON f.category_id = c.id
		WHERE f.id = ?
	`, feedID)

	var f Feed
	var catName string
	var catID sql.NullInt64
	var lastFetched sql.NullTime

	err := row.Scan(&f.ID, &f.Title, &f.FeedURL, &f.SiteURL, &catID, &catName,
		&f.ScheduleType, &f.ScheduleValue, &lastFetched, &f.LastError, &f.Etag, &f.LastModified, &f.CreatedAt)
	if err != nil {
		return nil, err
	}
	if catID.Valid {
		f.CategoryID = &catID.Int64
		f.CategoryName = catName
	}
	if lastFetched.Valid {
		f.LastFetchedAt = &lastFetched.Time
	}
	return &f, nil
}

func (d *DB) GetAllFeeds() ([]*Feed, error) {
	rows, err := d.Query(`
		SELECT f.id, f.title, f.feed_url, f.site_url, f.category_id, COALESCE(c.name, ''),
		       f.schedule_type, f.schedule_value, f.last_fetched_at, f.last_error, f.etag, f.last_modified, f.created_at,
		       (SELECT COUNT(*) FROM articles a 
		        LEFT JOIN article_states s ON a.id = s.article_id 
		        WHERE a.feed_id = f.id AND (s.is_read IS NULL OR s.is_read = 0)) as unread_count
		FROM feeds f
		LEFT JOIN categories c ON f.category_id = c.id
		ORDER BY f.id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var feeds []*Feed
	for rows.Next() {
		var f Feed
		var catName string
		var catID sql.NullInt64
		var lastFetched sql.NullTime

		if err := rows.Scan(&f.ID, &f.Title, &f.FeedURL, &f.SiteURL, &catID, &catName,
			&f.ScheduleType, &f.ScheduleValue, &lastFetched, &f.LastError, &f.Etag, &f.LastModified, &f.CreatedAt, &f.UnreadCount); err != nil {
			return nil, err
		}
		if catID.Valid {
			f.CategoryID = &catID.Int64
			f.CategoryName = catName
		}
		if lastFetched.Valid {
			f.LastFetchedAt = &lastFetched.Time
		}
		feeds = append(feeds, &f)
	}
	return feeds, nil
}

func (d *DB) UpdateFeedFetchStatus(feedID int64, lastFetched time.Time, lastError, etag, lastModified string) error {
	_, err := d.Exec(`
		UPDATE feeds SET last_fetched_at = ?, last_error = ?, etag = ?, last_modified = ?
		WHERE id = ?
	`, lastFetched, lastError, etag, lastModified, feedID)
	return err
}

// 分类管理
func (d *DB) GetAllCategories() ([]Category, error) {
	rows, err := d.Query("SELECT id, name, created_at FROM categories ORDER BY name ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cats []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name, &c.CreatedAt); err != nil {
			return nil, err
		}
		cats = append(cats, c)
	}
	return cats, nil
}

func (d *DB) GetOrCreateCategory(name string) (*Category, error) {
	if name == "" {
		return nil, nil
	}
	var c Category
	err := d.QueryRow("SELECT id, name, created_at FROM categories WHERE name = ?", name).Scan(&c.ID, &c.Name, &c.CreatedAt)
	if err == nil {
		return &c, nil
	}
	res, err := d.Exec("INSERT INTO categories (name) VALUES (?)", name)
	if err != nil {
		// 并发可能冲突，重试查询
		err = d.QueryRow("SELECT id, name, created_at FROM categories WHERE name = ?", name).Scan(&c.ID, &c.Name, &c.CreatedAt)
		if err != nil {
			return nil, err
		}
		return &c, nil
	}
	c.ID, _ = res.LastInsertId()
	c.Name = name
	c.CreatedAt = time.Now()
	return &c, nil
}

// 文章插入 (如果 guid 重复则忽略)
func (d *DB) SaveArticles(articles []*Article) (int, error) {
	if len(articles) == 0 {
		return 0, nil
	}
	tx, err := d.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO articles (feed_id, guid, title, url, content, author, published_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	newCount := 0
	for _, a := range articles {
		res, err := stmt.Exec(a.FeedID, a.GUID, a.Title, a.URL, a.Content, a.Author, a.PublishedAt)
		if err != nil {
			return 0, err
		}
		ra, _ := res.RowsAffected()
		if ra > 0 {
			newCount++
		}
	}
	return newCount, tx.Commit()
}

// 查询指定订阅源下的已入库文章列表 (供 Web 端查看数据库存储内容)
func (d *DB) GetFeedArticles(feedID int64, limit, offset int) ([]*Article, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	var total int
	err := d.QueryRow("SELECT COUNT(*) FROM articles WHERE feed_id = ?", feedID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := d.Query(`
		SELECT a.id, a.feed_id, f.title, a.guid, a.title, a.url, a.content, a.author, a.published_at, a.created_at,
		       COALESCE(s.is_read, 0) as is_read, COALESCE(s.is_starred, 0) as is_starred
		FROM articles a
		JOIN feeds f ON a.feed_id = f.id
		LEFT JOIN article_states s ON a.id = s.article_id
		WHERE a.feed_id = ?
		ORDER BY a.published_at DESC, a.id DESC
		LIMIT ? OFFSET ?
	`, feedID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var list []*Article
	for rows.Next() {
		var a Article
		var isReadInt, isStarredInt int
		if err := rows.Scan(&a.ID, &a.FeedID, &a.FeedTitle, &a.GUID, &a.Title, &a.URL, &a.Content, &a.Author,
			&a.PublishedAt, &a.CreatedAt, &isReadInt, &isStarredInt); err != nil {
			return nil, 0, err
		}
		a.IsRead = (isReadInt == 1)
		a.IsStarred = (isStarredInt == 1)
		list = append(list, &a)
	}
	return list, total, nil
}

// 用户认证
func (d *DB) GetUserByUsername(username string) (*User, error) {
	var u User
	err := d.QueryRow("SELECT id, username, password_hash, auth_token, created_at FROM users WHERE username = ?", username).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.AuthToken, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (d *DB) GetUserByToken(token string) (*User, error) {
	var u User
	err := d.QueryRow("SELECT id, username, password_hash, auth_token, created_at FROM users WHERE auth_token = ?", token).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.AuthToken, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (d *DB) UpdateUserToken(userID int64, token string) error {
	_, err := d.Exec("UPDATE users SET auth_token = ? WHERE id = ?", token, userID)
	return err
}

func (d *DB) UpdateUserPassword(userID int64, newPass string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(newPass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = d.Exec("UPDATE users SET password_hash = ? WHERE id = ?", string(hash), userID)
	return err
}
