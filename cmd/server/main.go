package main

import (
	"context"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"reader/internal/api/admin"
	"reader/internal/config"
	"reader/internal/db"
	"reader/internal/fetcher"
	"reader/internal/proxy"
	"reader/internal/scheduler"
	"reader/internal/web"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

func main() {
	cfg := config.Load()

	log.Printf("[Server] Initializing Reader (DataDir: %s, Port: %s)...", cfg.DataDir, cfg.Port)

	// 1. 初始化 SQLite 数据库
	database, err := db.InitDB(cfg.DBPath, cfg.AdminUser, cfg.AdminPass)
	if err != nil {
		log.Fatalf("[DB] Fatal initializing database: %v", err)
	}
	defer database.Close()
	log.Printf("[DB] SQLite database initialized at %s with WAL mode", cfg.DBPath)

	// 2. 初始化 RSS 抓取器与定时调度引擎 (拉取超时默认为 45s，可经 FETCH_TIMEOUT 配置)
	f := fetcher.NewFetcher(database, cfg.FetchTimeout)
	sched := scheduler.NewScheduler(database, f, cfg.FetchTimeout)
	sched.Start()
	defer sched.Stop()
	log.Printf("[Scheduler] Feed fetch timeout set to %ds", cfg.FetchTimeout)

	// 3. 构建 HTTP 路由
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// CORS 跨域支持
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// 高性能防盗链图片反代服务 (32KB 零拷贝流式转发 + 30 天客户端强缓存)
	imageProxy := proxy.NewImageProxy()
	r.Get("/api/proxy/image", imageProxy.ServeHTTP)

	// Web Admin API
	adminHandler := admin.NewAdminHandler(database, sched)
	r.Route("/api/admin", func(r chi.Router) {
		r.Post("/login", adminHandler.Login)
		r.Post("/logout", adminHandler.Logout)

		r.Group(func(r chi.Router) {
			r.Use(adminHandler.AuthMiddleware)
			r.Get("/profile", adminHandler.GetProfile)
			r.Get("/timeline", adminHandler.GetTimeline)
			r.Get("/feeds", adminHandler.GetFeeds)
			r.Post("/feeds", adminHandler.CreateFeed)
			r.Post("/feeds/batch", adminHandler.BatchAction)
			r.Post("/feeds/distribute-schedule", adminHandler.DistributeSchedule)
			r.Get("/feeds/{id}/articles", adminHandler.GetFeedArticles)
			r.Post("/articles/batch-read", adminHandler.BatchMarkArticlesRead)
			r.Post("/articles/batch-star", adminHandler.BatchMarkArticlesStarred)
			r.Post("/articles/mark-all-read", adminHandler.MarkAllTimelineRead)
			r.Put("/feeds/{id}", adminHandler.UpdateFeed)
			r.Delete("/feeds/{id}", adminHandler.DeleteFeed)
			r.Post("/feeds/{id}/fetch", adminHandler.FetchFeedNow)
			r.Post("/feeds/{id}/pause", adminHandler.ToggleFeedPause)
			r.Get("/categories", adminHandler.GetCategories)
			r.Post("/categories/update", adminHandler.UpdateCategory)
			r.Get("/tokens", adminHandler.GetSessions)
			r.Post("/tokens/revoke-others", adminHandler.RevokeOtherSessions)
			r.Delete("/tokens/{id}", adminHandler.RevokeSession)
		})
	})

	// 嵌入式 Web 控制台页面
	subFS, err := fs.Sub(web.DistFS, ".")
	if err == nil {
		fileServer := http.FileServer(http.FS(subFS))
		r.Handle("/*", fileServer)
	}

	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	go func() {
		log.Printf("[Server] Reader is running on http://0.0.0.0:%s", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[Server] Listen error: %v", err)
		}
	}()

	// 优雅停机监听
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("[Server] Shutting down Reader...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("[Server] Forced shutdown: %v", err)
	}
	log.Println("[Server] Reader gracefully stopped.")
}
