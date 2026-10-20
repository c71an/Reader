# Reader

Reader 是一个基于 Go 与 SQLite 构建的高性能轻量级自建 Feed 聚合与阅读服务，内嵌纯净美观的自适应 Web 客户端及高性能流式媒体反代。

## 系统特性

- **现代沉浸式 Web 端**：
  - 内嵌轻量静态单页应用（无前端复杂构建依赖，加载极快）。
  - 支持系统级暗黑模式（智能优化透明图表底衬、9GAG 风格极简视频播放）。
  - 支持多端响应式阅读、未读文章优先流式阅读、一键全读与分类管理。
- **任务调度引擎**：内置轻量级调度器，支持两种采集模式：
  - 间隔周期模式（Interval）
  - 每日固定时刻模式（Daily Fixed，支持配置多个时分点及随机抖动范围）
- **条件采集**：采集器支持 `ETag` 与 `Last-Modified` 条件请求头，减少冗余数据传输。
- **高性能流式媒体代理**：
  - 提供 `/api/proxy/image` 流式反向代理端点，基于 32KB 固定缓冲区进行零拷贝转发。
  - 支持基于规则的目标 Host 映射与请求头（Referer 等）注入机制，解决防盗链与图表显示问题。
  - 支持候选 URL 降级重试逻辑与客户端缓存控制（Cache-Control）。
  - 内置基于传输层 IP 校验的 SSRF 防护（拦截环回地址与私有网段）。
- **极简架构**：
  - 纯 Go 实现（无 CGO 依赖），单个二进制独立运行。
  - 数据持久化采用 SQLite（默认启用 WAL 模式及外键级联约束）。

> 💡 **提示**：如需使用 Google Reader API 对接第三方客户端（如 Reeder 等），可切换查看并使用 [`legacy`](https://github.com/c71an/Reader/tree/legacy) 分支。

---

## 部署运行

### Docker

```bash
docker run -d \
  --name reader \
  --restart unless-stopped \
  -p 8080:8080 \
  -e PORT=8080 \
  -e DATA_DIR=/data \
  -e ADMIN_USER=admin \
  -e ADMIN_PASSWORD=admin123 \
  -e TZ=Asia/Shanghai \
  -v ./data:/data \
  ghcr.io/c71an/reader:latest
```

### 本地编译运行

```bash
go run ./cmd/server
```

服务默认监听 `http://localhost:8080`，使用浏览器打开即可开始阅读。

---

## 配置参数

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `PORT` | `8080` | HTTP 服务监听端口 |
| `DATA_DIR` | `./data` | 数据持久化存储目录（包含 `reader.db`） |
| `ADMIN_USER` | `admin` | 管理员用户名 |
| `ADMIN_PASSWORD` | `admin123` | 管理员密码 |
| `FETCH_TIMEOUT` | `45` | 单个 Feed 采集请求超时时间（单位：秒） |
| `TZ` | `Asia/Shanghai` | 服务运行所在时区 |
