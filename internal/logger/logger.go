package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LogEntry 单条内存日志记录
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"` // INFO, WARN, ERROR, DEBUG
	Tag       string    `json:"tag"`   // Scheduler, Fetcher, Server, Admin, etc.
	Message   string    `json:"message"`
}

// Logger 结构体，支持：
// 1. 终端标准输出 (stdout，便于 docker logs)
// 2. 按天滚动文件输出 (/data/logs/reader-YYYY-MM-DD.log，自动清理 7 天前文件)
// 3. 内存环形缓冲区 (保留最近 500 条日志供前端实时查看)
type Logger struct {
	mu           sync.RWMutex
	logDir       string
	retention    time.Duration
	currentFile  *os.File
	currentDate  string
	recentLogs   []LogEntry
	maxRecent    int
	writers      io.Writer
	stopCleaning chan struct{}
}

var globalLogger *Logger

// Init 初始化全局日志记录器
func Init(dataDir string) (*Logger, error) {
	logDir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log dir: %w", err)
	}

	l := &Logger{
		logDir:       logDir,
		retention:    7 * 24 * time.Hour,
		recentLogs:   make([]LogEntry, 0, 500),
		maxRecent:    500,
		stopCleaning: make(chan struct{}),
	}

	if err := l.rotate(); err != nil {
		return nil, err
	}

	// 替换标准库 log 输出
	log.SetOutput(l)
	log.SetFlags(0) // 时间由我们统一格式化

	// 启动后台定时任务：每天清理超过 7 天的历史日志文件，并在跨天时轮转文件
	go l.scheduleMaintenance()

	globalLogger = l
	return l, nil
}

func Get() *Logger {
	return globalLogger
}

func (l *Logger) rotate() error {
	today := time.Now().Format("2006-01-02")
	if l.currentDate == today && l.currentFile != nil {
		return nil
	}

	if l.currentFile != nil {
		_ = l.currentFile.Close()
	}

	filePath := filepath.Join(l.logDir, fmt.Sprintf("reader-%s.log", today))
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file %s: %w", filePath, err)
	}

	l.currentFile = f
	l.currentDate = today
	l.writers = io.MultiWriter(os.Stdout, f)
	return nil
}

// Write 实现 io.Writer，拦截 log.Printf 或标准输出
func (l *Logger) Write(p []byte) (n int, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	today := now.Format("2006-01-02")
	if l.currentDate != today {
		_ = l.rotate()
	}

	msg := strings.TrimRight(string(p), "\r\n")
	if msg == "" {
		return len(p), nil
	}

	// 解析日志 tag 与 level，如 "[Scheduler] ..." 或 "[Fetcher] Fetching ... failed"
	level := "INFO"
	tag := "Server"

	lower := strings.ToLower(msg)
	if strings.Contains(lower, "failed") || strings.Contains(lower, "error") || strings.Contains(lower, "fatal") {
		level = "ERROR"
	} else if strings.Contains(lower, "warn") || strings.Contains(lower, "jitter") {
		level = "WARN"
	}

	// 识别类似 [Scheduler] 或 [Fetcher] 标签
	if strings.HasPrefix(msg, "[") {
		if end := strings.Index(msg, "]"); end > 1 {
			tag = msg[1:end]
		}
	}

	formattedLine := fmt.Sprintf("%s [%s] %s\n", now.Format("2006-01-02 15:04:05"), level, msg)

	if l.writers != nil {
		_, _ = l.writers.Write([]byte(formattedLine))
	} else {
		_, _ = os.Stdout.Write([]byte(formattedLine))
	}

	// 存入内存环形队列供前端 API 查看
	entry := LogEntry{
		Timestamp: now,
		Level:     level,
		Tag:       tag,
		Message:   msg,
	}

	if len(l.recentLogs) >= l.maxRecent {
		l.recentLogs = append(l.recentLogs[1:], entry)
	} else {
		l.recentLogs = append(l.recentLogs, entry)
	}

	return len(p), nil
}

// GetRecentLogs 获取最近的内存日志 (支持按 level, tag 过滤，最多 limit 条)
func (l *Logger) GetRecentLogs(limit int, levelFilter, tagFilter string) []LogEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if limit <= 0 || limit > l.maxRecent {
		limit = l.maxRecent
	}

	filtered := make([]LogEntry, 0, len(l.recentLogs))
	for i := len(l.recentLogs) - 1; i >= 0; i-- {
		entry := l.recentLogs[i]
		if levelFilter != "" && !strings.EqualFold(entry.Level, levelFilter) {
			continue
		}
		if tagFilter != "" && !strings.EqualFold(entry.Tag, tagFilter) {
			continue
		}
		filtered = append(filtered, entry)
		if len(filtered) >= limit {
			break
		}
	}

	// 反转回正序输出
	for i, j := 0, len(filtered)-1; i < j; i, j = i+1, j-1 {
		filtered[i], filtered[j] = filtered[j], filtered[i]
	}

	return filtered
}

// ClearRecentLogs 清空当前内存日志
func (l *Logger) ClearRecentLogs() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.recentLogs = make([]LogEntry, 0, l.maxRecent)
}

// CleanOldFiles 清除超过 7 天的历史日志文件
func (l *Logger) CleanOldFiles() {
	l.mu.Lock()
	defer l.mu.Unlock()

	entries, err := os.ReadDir(l.logDir)
	if err != nil {
		return
	}

	threshold := time.Now().Add(-l.retention)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "reader-") || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		// 检查修改时间是否超过 7 天
		if info.ModTime().Before(threshold) {
			filePath := filepath.Join(l.logDir, entry.Name())
			_ = os.Remove(filePath)
		}
	}
}

func (l *Logger) scheduleMaintenance() {
	ticker := time.NewTicker(2 * time.Hour)
	defer ticker.Stop()

	// 启动时先执行一次清理
	l.CleanOldFiles()

	for {
		select {
		case <-ticker.C:
			l.CleanOldFiles()
			l.mu.Lock()
			_ = l.rotate()
			l.mu.Unlock()
		case <-l.stopCleaning:
			return
		}
	}
}

func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()

	select {
	case <-l.stopCleaning:
	default:
		close(l.stopCleaning)
	}

	if l.currentFile != nil {
		_ = l.currentFile.Close()
		l.currentFile = nil
	}
}
