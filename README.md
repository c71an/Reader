# Reader

Reader 是一个基于 Go 与 SQLite 构建的轻量级 Feed 聚合与内容同步服务，提供标准 Google Reader API 兼容接口及内置流式媒体代理。

## 系统特性

- **协议兼容**：完整实现 Google Reader API 规范（`ClientLogin` 及 `/reader/api/0/*` 流与状态端点），支持跨客户端同步文章、已读状态与星标记录。
- **任务调度引擎**：内置轻量级调度器，支持两种采集模式：
  - 间隔周期模式（Interval）
  - 每日固定时刻模式（Daily Fixed，支持配置多个时分点及随机抖动范围）
- **条件采集**：采集器支持 `ETag` 与 `Last-Modified` 条件请求头，减少冗余数据传输。
- **流式媒体代理**：
  - 提供 `/api/proxy/image` 流式反向代理端点，基于 32KB 固定缓冲区进行零拷贝转发。
  - 支持基于规则的目标 Host 映射与请求头（Referer 等）注入机制。
  - 支持候选 URL 降级重试逻辑与客户端缓存控制（Cache-Control）。
  - 内置基于传输层 IP 校验的 SSRF 防护（拦截环回地址与私有网段）。
- **轻量化架构**：
  - 纯 Go 实现（无 CGO 依赖），内嵌静态 Web 管理控制台。
  - 数据持久化采用 SQLite（默认启用 WAL 模式及外键级联约束）。

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

服务默认监听 `http://localhost:8080`。

---

## API 与客户端接入

本服务支持标准 Google Reader API 客户端接入：

- **服务地址（Server）**：`http(s)://<HOST>:<PORT>`
- **账号（Username）**：环境变量 `ADMIN_USER`（默认 `admin`）
- **密码（Password）**：环境变量 `ADMIN_PASSWORD`（默认 `admin123`）

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
