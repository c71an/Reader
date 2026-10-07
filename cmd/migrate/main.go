package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reader/internal/db"

	_ "modernc.org/sqlite"
)

func main() {
	srcDBPath := flag.String("src", "", "FreshRSS 的 SQLite 数据库路径 (例如 users/admin/db.sqlite)")
	destDBPath := flag.String("dest", "./data/reader.db", "新 Reader 的 SQLite 数据库路径")
	importEntries := flag.Bool("articles", true, "是否同时导入历史文章及已读/标星状态 (默认 true)")
	defaultScheduleType := flag.String("schedule-type", "daily_fixed", "导入订阅的默认拉取调度类型 (daily_fixed 或 interval)")
	defaultScheduleVal := flag.String("schedule-value", "08:00", "导入订阅的默认调度参数 (如 08:00 或 60m)")
	flag.Parse()

	if *srcDBPath == "" {
		fmt.Println("用法: go run ./cmd/migrate -src <FreshRSS数据库路径> [-dest ./data/reader.db] [-articles=true]")
		fmt.Println("示例: go run ./cmd/migrate -src ./freshrss_db.sqlite")
		os.Exit(1)
	}

	if _, err := os.Stat(*srcDBPath); os.IsNotExist(err) {
		log.Fatalf("错误: 找不到源数据库文件: %s", *srcDBPath)
	}

	// 1. 初始化目标数据库 (自动执行建表与默认管理员)
	destDir := filepath.Dir(*destDBPath)
	_ = os.MkdirAll(destDir, 0755)
	targetDB, err := db.InitDB(*destDBPath, "admin", "admin123")
	if err != nil {
		log.Fatalf("初始化目标数据库失败: %v", err)
	}
	defer targetDB.Close()

	// 2. 连接源数据库 (只读连接)
	srcDB, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro", *srcDBPath))
	if err != nil {
		log.Fatalf("打开 FreshRSS 数据库失败: %v", err)
	}
	defer srcDB.Close()

	log.Printf(">>> 开始从 FreshRSS (%s) 迁移数据至 Reader (%s) ...\n", *srcDBPath, *destDBPath)

	// 获取默认管理员用户 ID
	var adminUserID int64 = 1
	_ = targetDB.QueryRow("SELECT id FROM users LIMIT 1").Scan(&adminUserID)

	// 3. 迁移分类文件夹 (category -> categories)
	catMapping, err := migrateCategories(srcDB, targetDB)
	if err != nil {
		log.Fatalf("分类迁移失败: %v", err)
	}

	// 4. 迁移订阅源 (feed -> feeds)
	feedMapping, err := migrateFeeds(srcDB, targetDB, catMapping, *defaultScheduleType, *defaultScheduleVal)
	if err != nil {
		log.Fatalf("订阅源迁移失败: %v", err)
	}

	// 5. 迁移文章与阅读状态 (entry -> articles, article_states)
	if *importEntries {
		if err := migrateArticles(srcDB, targetDB, feedMapping, adminUserID); err != nil {
			log.Fatalf("文章迁移失败: %v", err)
		}
	} else {
		log.Println(">>> 跳过文章迁移 (-articles=false)")
	}

	log.Println("==================================================")
	log.Println("🎉 FreshRSS 数据迁移全部完成！")
	log.Printf("数据已安全写入: %s\n", *destDBPath)
	log.Println("您可以启动 Reader 服务体验迁移后的数据。")
	log.Println("==================================================")
}

// 迁移分类文件夹
func migrateCategories(srcDB *sql.DB, targetDB *db.DB) (map[int64]int64, error) {
	log.Println(">>> [1/3] 正在迁移分类文件夹 (category) ...")
	rows, err := srcDB.Query("SELECT id, name FROM category")
	if err != nil {
		return nil, fmt.Errorf("查询源分类失败: %w", err)
	}
	defer rows.Close()

	mapping := make(map[int64]int64)
	importedCount := 0

	for rows.Next() {
		var oldID int64
		var name string
		if err := rows.Scan(&oldID, &name); err != nil {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		cat, err := targetDB.GetOrCreateCategory(name)
		if err != nil {
			log.Printf("  ⚠️ 插入分类失败 [%s]: %v", name, err)
			continue
		}
		mapping[oldID] = cat.ID
		importedCount++
	}

	log.Printf("  ✅ 成功迁移 %d 个分类文件夹\n", importedCount)
	return mapping, nil
}

// 迁移订阅源
func migrateFeeds(srcDB *sql.DB, targetDB *db.DB, catMapping map[int64]int64, schedType, schedVal string) (map[int64]int64, error) {
	log.Println(">>> [2/3] 正在迁移订阅源 (feed) ...")
	rows, err := srcDB.Query("SELECT id, name, url, website, category, lastUpdate FROM feed")
	if err != nil {
		return nil, fmt.Errorf("查询源订阅失败: %w", err)
	}
	defer rows.Close()

	mapping := make(map[int64]int64)
	importedCount := 0
	skippedCount := 0

	for rows.Next() {
		var oldID, oldCatID, lastUpdate sql.NullInt64
		var name, url, website sql.NullString

		if err := rows.Scan(&oldID, &name, &url, &website, &oldCatID, &lastUpdate); err != nil {
			continue
		}

		feedURL := strings.TrimSpace(url.String)
		if feedURL == "" {
			continue
		}

		feedTitle := strings.TrimSpace(name.String)
		if feedTitle == "" {
			feedTitle = feedURL
		}

		var targetCatID *int64
		if oldCatID.Valid && oldCatID.Int64 > 0 {
			if newCatID, exists := catMapping[oldCatID.Int64]; exists {
				targetCatID = &newCatID
			}
		}

		var lastFetched *time.Time
		if lastUpdate.Valid && lastUpdate.Int64 > 0 {
			t := time.Unix(lastUpdate.Int64, 0)
			lastFetched = &t
		}

		feed := &db.Feed{
			Title:         feedTitle,
			FeedURL:       feedURL,
			SiteURL:       website.String,
			CategoryID:    targetCatID,
			ScheduleType:  schedType,
			ScheduleValue: schedVal,
			LastFetchedAt: lastFetched,
		}

		err := targetDB.CreateFeed(feed)
		if err != nil {
			// 如果因为 feed_url 重复，查询已有 ID 映射
			var existingID int64
			errLookup := targetDB.QueryRow("SELECT id FROM feeds WHERE feed_url = ?", feedURL).Scan(&existingID)
			if errLookup == nil {
				mapping[oldID.Int64] = existingID
				skippedCount++
				continue
			}
			log.Printf("  ⚠️ 插入订阅失败 [%s]: %v", feedTitle, err)
			continue
		}

		mapping[oldID.Int64] = feed.ID
		importedCount++
	}

	log.Printf("  ✅ 成功迁移 %d 个订阅源 (跳过重复源 %d 个)\n", importedCount, skippedCount)
	return mapping, nil
}

// 迁移文章历史与已读/标星状态
func migrateArticles(srcDB *sql.DB, targetDB *db.DB, feedMapping map[int64]int64, adminUserID int64) error {
	log.Println(">>> [3/3] 正在迁移历史文章与状态 (entry -> articles, article_states) ...")

	var totalEntries int
	_ = srcDB.QueryRow("SELECT COUNT(*) FROM entry").Scan(&totalEntries)
	log.Printf("  源数据库共发现 %d 篇历史文章，正在批量处理...\n", totalEntries)

	rows, err := srcDB.Query(`
		SELECT id_feed, guid, title, author, content, link, date, is_read, is_favorite 
		FROM entry
		ORDER BY date DESC
	`)
	if err != nil {
		return fmt.Errorf("查询源文章失败: %w", err)
	}
	defer rows.Close()

	tx, err := targetDB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmtArticle, err := tx.Prepare(`
		INSERT OR IGNORE INTO articles (feed_id, guid, title, url, content, author, published_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmtArticle.Close()

	stmtState, err := tx.Prepare(`
		INSERT OR REPLACE INTO article_states (user_id, article_id, is_read, is_starred, read_at)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmtState.Close()

	articleCount := 0
	stateCount := 0
	batchSize := 2000

	for rows.Next() {
		var oldFeedID int64
		var guid, title, link sql.NullString
		var author, content sql.NullString
		var dateUnix int64
		var isRead, isFavorite int

		if err := rows.Scan(&oldFeedID, &guid, &title, &author, &content, &link, &dateUnix, &isRead, &isFavorite); err != nil {
			continue
		}

		newFeedID, exists := feedMapping[oldFeedID]
		if !exists {
			continue
		}

		cleanGuid := strings.TrimSpace(guid.String)
		if cleanGuid == "" {
			cleanGuid = link.String
		}
		if cleanGuid == "" {
			continue
		}

		pubTime := time.Unix(dateUnix, 0)

		res, err := stmtArticle.Exec(newFeedID, cleanGuid, title.String, link.String, content.String, author.String, pubTime)
		if err != nil {
			continue
		}

		articleID, _ := res.LastInsertId()
		if articleID == 0 {
			// 如果已存在，查询 article id
			_ = tx.QueryRow("SELECT id FROM articles WHERE feed_id = ? AND guid = ?", newFeedID, cleanGuid).Scan(&articleID)
		}

		if articleID > 0 {
			articleCount++
			if isRead > 0 || isFavorite > 0 {
				var readAt *time.Time
				if isRead > 0 {
					now := time.Now()
					readAt = &now
				}
				_, _ = stmtState.Exec(adminUserID, articleID, isRead, isFavorite, readAt)
				stateCount++
			}
		}

		if articleCount > 0 && articleCount%batchSize == 0 {
			log.Printf("  ... 已处理 %d 篇文章 (已读/收藏状态 %d 条)\n", articleCount, stateCount)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交数据库事务失败: %w", err)
	}

	log.Printf("  ✅ 成功迁移 %d 篇历史文章，同步状态 %d 条\n", articleCount, stateCount)
	return nil
}
