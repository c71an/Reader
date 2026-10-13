package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// RefererRule 定义来源域名到防盗链 Referer 的映射规则
type RefererRule struct {
	Pattern *regexp.Regexp
	Referer string
}

var defaultRefererRules = []RefererRule{
	// 微博 / 新浪图床
	{
		Pattern: regexp.MustCompile(`(?i)(?:sinaimg\.cn|weibo\.cn|weibocdn\.com)$`),
		Referer: "https://weibo.com/",
	},
	// 小红书图床
	{
		Pattern: regexp.MustCompile(`(?i)(?:xhscdn\.com|xiaohongshu\.com)$`),
		Referer: "https://www.xiaohongshu.com/",
	},
	// 知乎
	{
		Pattern: regexp.MustCompile(`(?i)zhimg\.com$`),
		Referer: "https://www.zhihu.com/",
	},
	// 哔哩哔哩
	{
		Pattern: regexp.MustCompile(`(?i)hdslb\.com$`),
		Referer: "https://www.bilibili.com/",
	},
	// 豆瓣
	{
		Pattern: regexp.MustCompile(`(?i)doubanio\.com$`),
		Referer: "https://www.douban.com/",
	},
}

var (
	// 小红书带时间戳与 MD5 Token 的临时链接正则
	// 例如: http://sns-webpic-qc.xhscdn.com/202610081146/acdc8493c5f7e534028a454e231c6737/notes_uhdr/1040g3qo...
	xhsTokenRegexp  = regexp.MustCompile(`(?i)^https?://([^/]+)/\d{10,14}/[0-9a-fA-F]{32}/(.+)$`)
	xhsVendorRegexp = regexp.MustCompile(`(?i)^sns-webpic-([a-zA-Z0-9]+)\.xhscdn\.com$`)
	xhsSimpleRegexp = regexp.MustCompile(`(?i)^https?://sns-webpic-([a-zA-Z0-9]+)\.xhscdn\.com/(.+)$`)
)

// ImageProxy 提供高性能、低内存占用的零拷贝图片反向代理服务
type ImageProxy struct {
	client *http.Client
	rules  []RefererRule
}

// NewImageProxy 创建新的图片反代引擎
func NewImageProxy() *ImageProxy {
	// 定制底层传输器：设置连接池与超时，且具备基础 SSRF 防护
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			// 校验 IP，拦截私有地址与回环地址，防止 SSRF
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if isPrivateOrLoopbackIP(ip.IP) {
					return nil, fmt.Errorf("forbidden destination IP: %s (SSRF protection)", ip.IP.String())
				}
			}
			dialer := &net.Dialer{Timeout: 8 * time.Second}
			return dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
		},
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true, // 直接透传二进制图片流
	}

	return &ImageProxy{
		client: &http.Client{
			Transport: transport,
			Timeout:   15 * time.Second,
		},
		rules: defaultRefererRules,
	}
}

// isPrivateOrLoopbackIP 检查是否为局域网或本地回环地址
func isPrivateOrLoopbackIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalMulticast() || ip.IsLinkLocalUnicast() {
		return true
	}
	// IPv4 私有段: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16
	if ip4 := ip.To4(); ip4 != nil {
		return ip4[0] == 10 ||
			(ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31) ||
			(ip4[0] == 192 && ip4[1] == 168) ||
			(ip4[0] == 169 && ip4[1] == 254) ||
			ip4[0] == 127
	}
	// IPv6 私有段: fc00::/7
	return len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc
}

// GetRefererForURL 根据目标 URL 域名匹配合法的 Referer
func (p *ImageProxy) GetRefererForURL(targetURL string) string {
	u, err := url.Parse(targetURL)
	if err != nil {
		return ""
	}
	hostname := u.Hostname()
	for _, rule := range p.rules {
		if rule.Pattern != nil && rule.Pattern.MatchString(hostname) {
			return rule.Referer
		}
	}
	return ""
}

// GetCandidateURLs 解析并生成候选链接（重点处理小红书短期带 Token 的链接降级）
func (p *ImageProxy) GetCandidateURLs(targetURL string) []string {
	var candidates []string

	// 1. 小红书短期 Token 链接识别: /202610081146/acdc.../notes_uhdr/...
	if m := xhsTokenRegexp.FindStringSubmatch(targetURL); len(m) == 3 {
		host := m[1]
		restPath := m[2]
		if vm := xhsVendorRegexp.FindStringSubmatch(host); len(vm) == 2 {
			candidates = append(candidates, fmt.Sprintf("https://sns-img-%s.xhscdn.com/%s", vm[1], restPath))
		}
		candidates = append(candidates, fmt.Sprintf("https://ci.xiaohongshu.com/%s", restPath))
	} else if vm := xhsSimpleRegexp.FindStringSubmatch(targetURL); len(vm) == 3 {
		candidates = append(candidates, fmt.Sprintf("https://sns-img-%s.xhscdn.com/%s", vm[1], vm[2]))
		candidates = append(candidates, fmt.Sprintf("https://ci.xiaohongshu.com/%s", vm[2]))
	}

	// 原始链接作为基础候选
	containsOriginal := false
	for _, c := range candidates {
		if c == targetURL {
			containsOriginal = true
			break
		}
	}
	if !containsOriginal {
		candidates = append(candidates, targetURL)
	}

	return candidates
}

// ServeHTTP 处理图片反向代理请求：GET /api/proxy/image?url=...
func (p *ImageProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rawTargetURL := strings.TrimSpace(r.URL.Query().Get("url"))
	if rawTargetURL == "" {
		http.Error(w, `Missing "url" query parameter`, http.StatusBadRequest)
		return
	}

	// 防套娃解包：如果传入了嵌套的代理 URL，自动递归解包至最深层的原始图片真实地址
	for {
		if strings.Contains(rawTargetURL, "/api/proxy/image?url=") || strings.Contains(rawTargetURL, "/proxy?url=") {
			uInner, err := url.Parse(rawTargetURL)
			if err == nil {
				innerQ := uInner.Query().Get("url")
				if innerQ != "" && innerQ != rawTargetURL {
					rawTargetURL = strings.TrimSpace(innerQ)
					continue
				}
			}
		}
		break
	}

	u, err := url.Parse(rawTargetURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		http.Error(w, `Invalid target URL scheme, only http/https allowed`, http.StatusBadRequest)
		return
	}

	candidates := p.GetCandidateURLs(rawTargetURL)
	var upstreamResp *http.Response

	for _, currentURL := range candidates {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, currentURL, nil)
		if err != nil {
			continue
		}

		// 模拟桌面现代浏览器 UA
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")

		// 动态注入匹配的防盗链 Referer
		if ref := p.GetRefererForURL(currentURL); ref != "" {
			req.Header.Set("Referer", ref)
		}

		resp, err := p.client.Do(req)
		if err != nil {
			continue
		}

		if resp.StatusCode == http.StatusOK {
			upstreamResp = resp
			break
		}
		_ = resp.Body.Close()
	}

	if upstreamResp == nil {
		http.Error(w, "Failed to fetch image from upstream candidates", http.StatusBadGateway)
		return
	}
	defer upstreamResp.Body.Close()

	// 透传 Content-Type，默认为 image/jpeg
	contentType := upstreamResp.Header.Get("Content-Type")
	if contentType == "" || strings.HasPrefix(contentType, "text/") {
		contentType = "image/jpeg"
	}
	w.Header().Set("Content-Type", contentType)

	// 透传 Content-Length
	if cl := upstreamResp.Header.Get("Content-Length"); cl != "" {
		w.Header().Set("Content-Length", cl)
	}

	// 核心强缓存头：指示浏览器客户端（手机 Safari 等）在本地闪存强缓存 30 天，服务器无需承担任何缓存内存
	w.Header().Set("Cache-Control", "public, max-age=2592000, immutable")

	// 零拷贝流水线转发：仅占用 32KB 流缓冲区，流完即止
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, upstreamResp.Body)
}
