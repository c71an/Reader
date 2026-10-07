package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
}

type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	AuthToken    string    `json:"auth_token"`
	CreatedAt    time.Time `json:"created_at"`
}

type Category struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type Feed struct {
	ID            int64      `json:"id"`
	Title         string     `json:"title"`
	FeedURL       string     `json:"feed_url"`
	SiteURL       string     `json:"site_url"`
	CategoryID    *int64     `json:"category_id"`
	CategoryName  string     `json:"category_name,omitempty"`
	ScheduleType  string     `json:"schedule_type"`  // "interval" 或 "daily_fixed"
	ScheduleValue string     `json:"schedule_value"` // "30m", "1h" 或 "08:00,18:30"
	LastFetchedAt *time.Time `json:"last_fetched_at"`
	LastError     string     `json:"last_error"`
	Etag          string     `json:"etag"`
	LastModified  string     `json:"last_modified"`
	UnreadCount   int        `json:"unread_count,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

type Article struct {
	ID          int64     `json:"id"`
	FeedID      int64     `json:"feed_id"`
	FeedTitle   string    `json:"feed_title,omitempty"`
	GUID        string    `json:"guid"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	Content     string    `json:"content"`
	Author      string    `json:"author"`
	PublishedAt time.Time `json:"published_at"`
	CreatedAt   time.Time `json:"created_at"`
	IsRead      bool      `json:"is_read"`
	IsStarred   bool      `json:"is_starred"`
}

func InitDB(dbPath, defaultUser, defaultPass string) (*DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", dbPath)
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// 限制单个写锁连接池体验最佳
	database.SetMaxOpenConns(25)
	database.SetMaxIdleConns(5)
	database.SetConnMaxLifetime(time.Hour)

	d := &DB{database}
	if err := d.migrate(); err != nil {
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	if err := d.ensureDefaultUser(defaultUser, defaultPass); err != nil {
		return nil, fmt.Errorf("ensure default user failed: %w", err)
	}

	return d, nil
}

func (d *DB) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		auth_token TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS categories (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS feeds (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL,
		feed_url TEXT UNIQUE NOT NULL,
		site_url TEXT DEFAULT '',
		category_id INTEGER,
		schedule_type TEXT NOT NULL DEFAULT 'interval',
		schedule_value TEXT NOT NULL DEFAULT '60m',
		last_fetched_at DATETIME,
		last_error TEXT DEFAULT '',
		etag TEXT DEFAULT '',
		last_modified TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE SET NULL
	);

	CREATE TABLE IF NOT EXISTS articles (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		feed_id INTEGER NOT NULL,
		guid TEXT NOT NULL,
		title TEXT NOT NULL,
		url TEXT NOT NULL,
		content TEXT DEFAULT '',
		author TEXT DEFAULT '',
		published_at DATETIME NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(feed_id, guid),
		FOREIGN KEY (feed_id) REFERENCES feeds(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS article_states (
		user_id INTEGER NOT NULL,
		article_id INTEGER NOT NULL,
		is_read INTEGER DEFAULT 0,
		is_starred INTEGER DEFAULT 0,
		read_at DATETIME,
		PRIMARY KEY(user_id, article_id),
		FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
		FOREIGN KEY (article_id) REFERENCES articles(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_articles_published ON articles(published_at DESC);
	CREATE INDEX IF NOT EXISTS idx_articles_feed ON articles(feed_id);
	CREATE INDEX IF NOT EXISTS idx_states_user_read ON article_states(user_id, is_read);
	CREATE INDEX IF NOT EXISTS idx_states_user_starred ON article_states(user_id, is_starred);
	`
	_, err := d.Exec(schema)
	return err
}

func (d *DB) ensureDefaultUser(username, password string) error {
	var count int
	err := d.QueryRow("SELECT COUNT(*) FROM users WHERE username = ?", username).Scan(&count)
	if err != nil {
		return err
	}
	if count == 0 {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		// 初始 authToken
		authToken := fmt.Sprintf("reader_token_%d", time.Now().UnixNano())
		_, err = d.Exec("INSERT INTO users (username, password_hash, auth_token) VALUES (?, ?, ?)", username, string(hash), authToken)
		return err
	}
	return nil
}

