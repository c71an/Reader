package db

import (
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
