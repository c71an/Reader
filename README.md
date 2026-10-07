# Reader - 自定义 RSS 阅读器服务

专为 RSSHub 定制、集成 SQLite、完全兼容 Reeder 客户端 (Google Reader API)、支持单订阅按天固定时刻拉取的极轻量 RSS 服务。

## ✨ 特性一览
1. **RSSHub 友好**：支持自定义抓取请求头，避免防爬拦截；支持 ETag / Last-Modified 缓存协议，大幅减少无效请求与流量消耗。
2. **每个订阅独立配置每日固定时间拉取**：
   - 可配置为 `daily_fixed` 模式（例如设置 `08:00` 或 `08:00,18:30`），专为日更/周更或易被频繁抓取限制的源设计。
   - 亦可配置为传统 `interval` 模式（如 `30m`、`1h`）。
3. **Reeder 原生支持**：
   - 完整实现 Google Reader API 标准协议（`ClientLogin` 授权，`/reader/api/0/*`）。
   - 在 Reeder 客户端中添加账号类型为 "Google Reader" 或 "FreshRSS" 即可直接同步。
   - 支持已读、未读、标星收藏状态实时双向同步。
4. **嵌入式现代化 Web 管理后台**：
   - 单二进制打包，零外部 Node 依赖。
   - 管理订阅源、修改拉取时间、即时手动抓取、查看抓取错误与状态。
5. **单容器 + SQLite WAL 极简部署**：
   - 纯 Go 编写（无需 CGO），内存占用极低（~15MB）。
   - 自动开启 SQLite WAL 模式，高并发读写极佳。

---

## 🚀 快速启动

### 方式一：Docker Compose（推荐）
```yaml
services:
  reader:
    # 官方自动构建镜像 (支持 linux/amd64 与 linux/arm64)
    image: ghcr.io/c71an/reader:latest
    container_name: reader
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      - PORT=8080
      - DATA_DIR=/data
      - ADMIN_USER=admin
      - ADMIN_PASSWORD=admin123
      - TZ=Asia/Shanghai
    volumes:
      - ./data:/data
```
运行：
```bash
docker compose up -d
```

### 方式二：本地运行 (Go)
```bash
go run ./cmd/server
# 访问 http://localhost:8080
```

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
| `TZ` | `Asia/Shanghai` | 时区设置 (确保每日固定时间拉取准确匹配本地时间) |

