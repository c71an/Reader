package fetcher

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"reader/internal/db"

	"github.com/mmcdole/gofeed"
)

type Fetcher struct {
	db         *db.DB
	httpClient *http.Client
	fp         *gofeed.Parser
	userAgent  string
}

func NewFetcher(database *db.DB, timeoutSeconds int) *Fetcher {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 45
	}
	timeout := time.Duration(timeoutSeconds) * time.Second

	// 针对 RSSHub 和常规 Feed 优化 HTTP Client 配置
	tr := &http.Transport{
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: false},
		MaxIdleConns:          50,
		IdleConnTimeout:       60 * time.Second,
		ResponseHeaderTimeout: timeout,
	}

	client := &http.Client{
		Transport: tr,
		Timeout:   timeout,
	}

	fp := gofeed.NewParser()
	fp.Client = client

	return &Fetcher{
		db:         database,
		httpClient: client,
		fp:         fp,
		userAgent:  "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Reader/1.0 RSSHub-Friendly",
	}
}

// FetchFeed 抓取单个订阅，处理条件请求、解析与入库
func (f *Fetcher) FetchFeed(ctx context.Context, feed *db.Feed) (int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", feed.FeedURL, nil)
	if err != nil {
		return 0, err
	}

	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "application/atom+xml, application/rss+xml, application/xml, text/xml, application/json, */*")

	// 缓存支持
	if feed.Etag != "" {
		req.Header.Set("If-None-Match", feed.Etag)
	}
	if feed.LastModified != "" {
		req.Header.Set("If-Modified-Since", feed.LastModified)
	}

	resp, err := f.httpClient.Do(req)
	if err != nil {
		now := time.Now()
		_ = f.db.UpdateFeedFetchStatus(feed.ID, now, err.Error(), feed.Etag, feed.LastModified)
		return 0, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		// 内容未变
		now := time.Now()
		_ = f.db.UpdateFeedFetchStatus(feed.ID, now, "", feed.Etag, feed.LastModified)
		return 0, nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errStr := fmt.Sprintf("unexpected http status: %s", resp.Status)
		now := time.Now()
		_ = f.db.UpdateFeedFetchStatus(feed.ID, now, errStr, feed.Etag, feed.LastModified)
		return 0, fmt.Errorf("%s", errStr)
	}

	// 限制响应体最多读取 10MB，防止恶意异常大响应耗尽内存
	parsedFeed, err := f.fp.Parse(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		now := time.Now()
		_ = f.db.UpdateFeedFetchStatus(feed.ID, now, fmt.Sprintf("parse error: %v", err), feed.Etag, feed.LastModified)
		return 0, fmt.Errorf("feed parsing failed: %w", err)
	}

	// 提取并更新 Feed 元数据 (标题，网站地址等)
	newTitle := feed.Title
	if newTitle == "" || newTitle == feed.FeedURL {
		if parsedFeed.Title != "" {
			newTitle = strings.TrimSpace(parsedFeed.Title)
		}
	}
	newSiteURL := feed.SiteURL
	if newSiteURL == "" && parsedFeed.Link != "" {
		newSiteURL = strings.TrimSpace(parsedFeed.Link)
	}

	if newTitle != feed.Title || newSiteURL != feed.SiteURL {
		feed.Title = newTitle
		feed.SiteURL = newSiteURL
		_ = f.db.UpdateFeed(feed)
	}

	// 转换文章实体
	now := time.Now()
	var articles []*db.Article
	for _, item := range parsedFeed.Items {
		pubDate := now
		if item.PublishedParsed != nil {
			pubDate = *item.PublishedParsed
		} else if item.UpdatedParsed != nil {
			pubDate = *item.UpdatedParsed
		}

		guid := strings.TrimSpace(item.GUID)
		if guid == "" {
			guid = strings.TrimSpace(item.Link)
		}
		if guid == "" {
			guid = strings.TrimSpace(item.Title)
		}
		if guid == "" {
			continue
		}

		content := item.Content
		if content == "" {
			content = item.Description
		}

		author := ""
		if item.Author != nil {
			author = item.Author.Name
		}

		articles = append(articles, &db.Article{
			FeedID:      feed.ID,
			GUID:        guid,
			Title:       strings.TrimSpace(item.Title),
			URL:         strings.TrimSpace(item.Link),
			Content:     content,
			Author:      author,
			PublishedAt: pubDate,
		})
	}

	newCount, err := f.db.SaveArticles(articles)
	if err != nil {
		return 0, fmt.Errorf("failed to save articles: %w", err)
	}

	etag := resp.Header.Get("ETag")
	lastMod := resp.Header.Get("Last-Modified")
	_ = f.db.UpdateFeedFetchStatus(feed.ID, now, "", etag, lastMod)

	return newCount, nil
}

