package db

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestForeignKeysCascade(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_fk.db")

	database, err := InitDB(dbPath, "admin", "admin123")
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer database.Close()

	// Check if foreign keys are enabled
	var fkEnabled int
	if err := database.QueryRow("PRAGMA foreign_keys").Scan(&fkEnabled); err != nil {
		t.Fatalf("Query PRAGMA foreign_keys failed: %v", err)
	}
	t.Logf("PRAGMA foreign_keys = %d", fkEnabled)

	// Create a feed
	feed := &Feed{
		Title:         "Test Feed",
		FeedURL:       "https://example.com/rss",
		ScheduleType:  "cron",
		ScheduleValue: "0 8 *",
	}
	if err := database.CreateFeed(feed); err != nil {
		t.Fatalf("CreateFeed failed: %v", err)
	}
	feedID := feed.ID

	// Insert articles
	articles := []*Article{
		{
			FeedID:      feedID,
			GUID:        "guid-1",
			Title:       "Article 1",
			URL:         "https://example.com/1",
			Content:     "Hello",
			PublishedAt: time.Now(),
		},
	}
	if _, err := database.SaveArticles(articles); err != nil {
		t.Fatalf("SaveArticles failed: %v", err)
	}

	// Delete feed
	if err := database.DeleteFeed(feedID); err != nil {
		t.Fatalf("DeleteFeed failed: %v", err)
	}

	// Check if articles are deleted
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM articles WHERE feed_id = ?", feedID).Scan(&count); err != nil {
		t.Fatalf("Query articles count failed: %v", err)
	}

	if count != 0 {
		t.Errorf("Expected 0 articles after DeleteFeed, but found %d orphan articles remaining!", count)
	}
}

func TestPaginationContinuation(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_pag.db")

	database, err := InitDB(dbPath, "admin", "admin123")
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer database.Close()

	user, _ := database.GetUserByUsername("admin")

	feed := &Feed{
		Title:   "Feed 1",
		FeedURL: "https://example.com/rss",
	}
	if err := database.CreateFeed(feed); err != nil {
		t.Fatalf("CreateFeed failed: %v", err)
	}

	baseTime := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	var articles []*Article
	for i := 1; i <= 20; i++ {
		articles = append(articles, &Article{
			FeedID:      feed.ID,
			GUID:        fmt.Sprintf("guid-%d", i),
			Title:       fmt.Sprintf("Article %d", i),
			URL:         fmt.Sprintf("https://example.com/%d", i),
			Content:     "Body",
			PublishedAt: baseTime.Add(time.Duration(i) * time.Hour), // 1h, 2h, ...
		})
	}
	if _, err := database.SaveArticles(articles); err != nil {
		t.Fatalf("SaveArticles failed: %v", err)
	}

	// Page 1 (limit 7)
	p1, err := database.GetStreamItems(StreamQueryFilter{
		UserID:   user.ID,
		StreamID: "user/-/state/com.google/reading-list",
		Limit:    7,
	})
	if err != nil {
		t.Fatalf("Page 1 failed: %v", err)
	}
	if len(p1) != 7 {
		t.Fatalf("Expected 7 items in Page 1, got %d", len(p1))
	}

	lastP1 := p1[len(p1)-1]
	cont := fmt.Sprintf("%d_%d", lastP1.PublishedAt.Unix(), lastP1.ID)
	t.Logf("Page 1 last: ID=%d, Title=%s, Pub=%v, Cont=%s", lastP1.ID, lastP1.Title, lastP1.PublishedAt, cont)

	// Page 2 (limit 7)
	p2, err := database.GetStreamItems(StreamQueryFilter{
		UserID:       user.ID,
		StreamID:     "user/-/state/com.google/reading-list",
		Limit:        7,
		Continuation: cont,
	})
	if err != nil {
		t.Fatalf("Page 2 failed: %v", err)
	}
	t.Logf("Page 2 length: %d", len(p2))
	for _, it := range p2 {
		t.Logf("Page 2 item: ID=%d, Title=%s, Pub=%v", it.ID, it.Title, it.PublishedAt)
		for _, it1 := range p1 {
			if it.ID == it1.ID {
				t.Errorf("Duplicate item between page 1 and page 2: ID=%d, Title=%s", it.ID, it.Title)
			}
		}
	}
}

