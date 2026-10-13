# Reader - 自定义 RSS 阅读器服务

专为 RSSHub 定制、集成 SQLite、完全兼容 Reeder 客户端 (Google Reader API)、内置高性能防盗链图片反代、支持单订阅按天固定时刻拉取的极轻量 RSS 服务。

## ✨ 特性一览
1. **RSSHub 友好**：支持自定义抓取请求头，避免防爬拦截；支持 ETag / Last-Modified 缓存协议，大幅减少无效请求与流量消耗。
2. **内置高性能防盗链图片反代**：
   - 原生提供 `/api/proxy/image` 零拷贝流式反代端点，仅占用 32KB 流缓冲区，告别 Node.js 代理的百兆内存暴涨。
   - 自动匹配微博、小红书、知乎、B站、豆瓣等图床合法 Referer 绕过 403 裂图。
   - 小红书临时带 Token 链接自动降级并重试永久候选地址。
   - 响应注入 30 天客户端强缓存头，iOS Reeder / 浏览器本地持久化缓存，不耗费服务器额外内存。
3. **每个订阅独立配置每日固定时间拉取**：
   - 可配置为 `daily_fixed` 模式（例如设置 `08:00` 或 `08:00,18:30`，支持随机抖动 Jitter），专为日更/周更或易被频繁抓取限制的源设计。
   - 亦可配置为传统 `interval` 模式（如 `30m`、`1h`）。
4. **Reeder 原生支持**：
   - 完整实现 Google Reader API 标准协议（`ClientLogin` 授权，`/reader/api/0/*`）。
   - 在 Reeder 客户端中添加账号类型为 "Google Reader" 或 "FreshRSS" 即可直接同步。
   - 支持已读、未读、标星收藏状态实时双向同步。
5. **嵌入式现代化 Web 管理后台**：
   - 单二进制打包，零外部 Node 依赖。
   - 管理订阅源、修改拉取时间、即时手动抓取、查看抓取错误与状态。
6. **单容器 + SQLite WAL 极简部署**：
   - 纯 Go 编写（无需 CGO），内存占用极低（~15MB）。
   - 自动开启 SQLite WAL 模式与外键约束级联删除，数据安全可靠。

---

## 🚀 快速启动

### 方式一：Docker 命令运行（推荐）

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

### 方式二：本地运行 (Go)

```bash
go run ./cmd/server
# 访问 http://localhost:8080
```

---

## 🖼️ 图片防盗链反代机制

项目内置了针对防盗链图床的原生高性能流式反向代理端点（`/api/proxy/image`）：

1. **动态 Referer 注入**：
   - 微博图床（`*.sinaimg.cn`, `*.weibocdn.com` 等）自动附带 `Referer: https://weibo.com/`。
   - 小红书图床（`*.xhscdn.com`, `*.xiaohongshu.com`）自动附带 `Referer: https://www.xiaohongshu.com/`。
   - 知乎（`zhimg.com`）、B站（`hdslb.com`）、豆瓣（`doubanio.com`）等自动注入对应平台合法 Referer。
2. **小红书临时链接降级重试**：
   - 自动识别携带时间戳及临时 Token 的过期链接，解析并自动降级为永久 CDN 候选地址重试，杜绝裂图。
3. **零拷贝流式转发（Zero-Copy）**：
   - 单次代理仅占用 32KB 流缓冲区，数据流即收即发，不落地硬盘，不堆积内存，峰值并发内存占用极低。
4. **客户端 30 天强缓存**：
   - 注入 `Cache-Control: public, max-age=2592000, immutable`，指示手机 iOS Reeder 或浏览器在本地闪存直接强缓存 30 天，二次阅读秒开且不耗费服务器资源。
5. **SSRF 安全防护**：
   - 底层传输拨号器自动拦截本地回环（`127.0.0.1`）及所有内网私有网段（`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`）。

### 与 RSSHub / Router 配合使用

若希望在抓取源头即完成链接改写，以便在 iOS Reeder 中直接免裂图阅读，可在 RSSHub 启动环境中配置指向本服务的代理：

```bash
-e HOTLINK_TEMPLATE='https://reader.yourdomain.com/api/proxy/image?url=${href_ue}' \
-e HOTLINK_INCLUDE_PATHS='/weibo,/xiaohongshu'
```
*(注意：请将地址替换为您实际部署 Reader 的外部可访问域名或 IP 端口，在 Linux Shell 中使用单引号以防止 `${href_ue}` 被意外转义)*

---

## 📱 Reeder 客户端连接指引
1. 打开 Reeder (iOS / macOS)，在 Accounts 中选择添加 **Google Reader**。
2. 配置参数：
   - **Server**：`http://你的服务器IP或域名:8080`
   - **Username**：`admin` (或环境变量 `ADMIN_USER`)
   - **Password**：`admin123` (或环境变量 `ADMIN_PASSWORD`)
3. 保存后 Reeder 将会自动同步文章列表、分类目录与已读/标星状态。

---

## ⚙️ 环境变量说明
| 变量名 | 默认值 | 描述 |
|---|---|---|
| `PORT` | `8080` | Web 与 API 监听端口 |
| `DATA_DIR` | `./data` | 数据持久化存储路径 (包含 reader.db) |
| `ADMIN_USER` | `admin` | 默认管理员账号 (用于 Web 与 Reeder 登录) |
| `ADMIN_PASSWORD` | `admin123` | 默认管理员密码 |
| `FETCH_TIMEOUT` | `45` | 单个 Feed 抓取超时时间 (单位：秒，推荐 30~60s) |
| `TZ` | `Asia/Shanghai` | 时区设置 (确保每日固定时间拉取准确匹配本地时间) |
