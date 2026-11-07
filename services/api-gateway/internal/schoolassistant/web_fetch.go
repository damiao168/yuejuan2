package schoolassistant

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const (
	webPageMaxBytes    = 2 << 20
	webPageMaxRunes    = 12_000
	webPageReadTimeout = 10 * time.Second
)

var errWebPageUnavailable = errors.New("public page content unavailable")

// 诊断仅包含固定阶段和状态码，不能把网址、请求头或上游响应正文写进错误。
func webPageFailure(stage string) error { return fmt.Errorf("%w: %s", errWebPageUnavailable, stage) }

// WebPageReader 只读取编排层从真实搜索候选中选中的网址，不接受任意模型生成的访问地址。
// 同一轮最多并发三次 Read；实现须支持并发调用，并在 context 取消后终止网络读取。
type WebPageReader interface {
	Read(context.Context, string) (WebPageContent, error)
}

// WebPageContent 是有上限的提取文本；读取器不执行脚本，也不携带登录 Cookie。
// 网页文字仍是不可信材料；读取时间不等于网页发布时间或天气观测时间。
type WebPageContent struct {
	Text   string
	Title  string
	URL    string
	ReadAt time.Time
}

type publicWebPageReader struct{ client *http.Client }

// NewPublicWebPageReader 不带浏览器会话、模型密钥或环境代理；每次拨号先验证 DNS 全部结果再固定 IP。
// 最多跟随两次公开 HTTP(S) 跳转，跳转后的地址也必须重新经过 URL 和拨号检查。
func NewPublicWebPageReader() WebPageReader {
	transport := &http.Transport{
		DialContext: dialPublicWebPage, TLSHandshakeTimeout: 5 * time.Second,
		ResponseHeaderTimeout: webPageReadTimeout, IdleConnTimeout: 20 * time.Second,
		MaxIdleConns: 8, MaxIdleConnsPerHost: 2, MaxResponseHeaderBytes: 32 << 10,
	}
	return &publicWebPageReader{client: &http.Client{
		Transport: transport, Timeout: webPageReadTimeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) > 2 || !validWebPageURL(request.URL.String()) {
				return errWebPageUnavailable
			}
			return nil
		},
	}}
}

func validWebPageURL(address string) bool {
	_, _, ok := publicSearchURL(address)
	if !ok {
		return false
	}
	u, err := url.Parse(address)
	if err != nil || (u.Port() != "" && u.Port() != "80" && u.Port() != "443") {
		return false
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil {
		return publicWebPageIP(ip)
	}
	return true
}

var webPageSpecialNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.31.196.0/24"), netip.MustParsePrefix("192.52.193.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("192.175.48.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("2620:4f:8000::/48"),
	netip.MustParsePrefix("3fff::/20"),
}

func publicWebPageIP(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() {
		return false
	}
	if address.Is6() && !netip.MustParsePrefix("2000::/3").Contains(address) {
		return false
	}
	for _, prefix := range webPageSpecialNetworks {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func dialPublicWebPage(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || (port != "80" && port != "443") {
		return nil, errWebPageUnavailable
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, webPageFailure("dns")
	}
	return dialResolvedPublicWebPage(ctx, network, port, ips, (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 15 * time.Second}).DialContext)
}

func dialResolvedPublicWebPage(ctx context.Context, network, port string, ips []netip.Addr, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	if len(ips) == 0 {
		return nil, errWebPageUnavailable
	}
	// 检查整个 DNS 回答后才连接固定 IP，防止混合内外网地址和二次解析绕过范围验证。
	for _, ip := range ips {
		if !publicWebPageIP(ip) {
			return nil, webPageFailure("non_public_dns")
		}
	}
	for _, ip := range ips {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		connection, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return connection, nil
		}
	}
	return nil, errWebPageUnavailable
}

func (reader *publicWebPageReader) Read(ctx context.Context, address string) (WebPageContent, error) {
	if err := ctx.Err(); err != nil {
		return WebPageContent{}, err
	}
	if !validWebPageURL(address) {
		return WebPageContent{}, webPageFailure("url")
	}
	ctx, cancel := context.WithTimeout(ctx, webPageReadTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return WebPageContent{}, webPageFailure("request")
	}
	request.Header.Set("User-Agent", "EduGrade-PublicReader/1.0")
	request.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.7")
	response, err := reader.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return WebPageContent{}, ctx.Err()
		}
		return WebPageContent{}, webPageFailure("transport")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return WebPageContent{}, webPageFailure(fmt.Sprintf("http_%d", response.StatusCode))
	}
	if response.ContentLength > webPageMaxBytes {
		return WebPageContent{}, webPageFailure("size")
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || (mediaType != "text/html" && mediaType != "application/xhtml+xml" && mediaType != "text/plain") {
		return WebPageContent{}, webPageFailure("media_type")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, webPageMaxBytes+1))
	if err != nil || len(raw) > webPageMaxBytes {
		return WebPageContent{}, webPageFailure("body")
	}
	decoded, err := charset.NewReader(bytes.NewReader(raw), response.Header.Get("Content-Type"))
	if err != nil {
		return WebPageContent{}, webPageFailure("charset")
	}
	data, err := io.ReadAll(io.LimitReader(decoded, webPageMaxBytes+1))
	if err != nil || len(data) > webPageMaxBytes {
		return WebPageContent{}, webPageFailure("decoded_body")
	}
	finalURL := address
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL.String()
	}
	content := WebPageContent{URL: finalURL, ReadAt: time.Now().UTC()}
	if mediaType == "text/plain" {
		content.Text = truncatePageText(string(data), webPageMaxRunes)
	} else {
		content.Title, content.Text, err = extractWebPage(data)
		if err != nil {
			return WebPageContent{}, err
		}
	}
	if utf8.RuneCountInString(strings.TrimSpace(content.Text)) < 24 {
		return WebPageContent{}, webPageFailure("empty_body")
	}
	return content, nil
}

func truncatePageText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit]) + "\n[正文超出读取上限，后续未读取]"
	}
	return value
}

func hiddenWebPageNode(node *html.Node) bool {
	if node.Type != html.ElementNode {
		return false
	}
	switch node.Data {
	case "script", "style", "title", "nav", "header", "footer", "form", "svg", "template", "noscript", "iframe", "object", "canvas":
		return true
	}
	for _, attr := range node.Attr {
		if attr.Key == "hidden" || (attr.Key == "aria-hidden" && attr.Val == "true") {
			return true
		}
		if attr.Key == "style" {
			style := strings.ReplaceAll(strings.ToLower(attr.Val), " ", "")
			if strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden") {
				return true
			}
		}
	}
	return false
}

func webPageNodeText(node *html.Node, depth int, output *strings.Builder) {
	if depth > 128 || output.Len() > webPageMaxBytes || hiddenWebPageNode(node) {
		return
	}
	if node.Type == html.TextNode {
		output.WriteString(node.Data)
		output.WriteByte(' ')
	}
	if node.Type == html.ElementNode && node.Data == "time" {
		for _, attr := range node.Attr {
			if attr.Key == "datetime" {
				output.WriteString(attr.Val)
				output.WriteByte(' ')
			}
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		webPageNodeText(child, depth+1, output)
	}
	if node.Type == html.ElementNode {
		switch node.Data {
		case "p", "div", "section", "article", "main", "br", "tr", "li", "h1", "h2", "h3", "h4", "table":
			output.WriteByte('\n')
		}
	}
}

func extractWebPage(data []byte) (string, string, error) {
	document, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return "", "", errWebPageUnavailable
	}
	title := ""
	var body *html.Node
	var explicit []*html.Node
	count := 0
	var visit func(*html.Node, int)
	visit = func(node *html.Node, depth int) {
		count++
		if count > 50_000 || depth > 128 {
			return
		}
		if node.Type == html.ElementNode {
			// 某些门户把广告错误片段嵌进正文，片段内的第二个 title 不能覆盖文档标题。
			if title == "" && node.Data == "title" && node.Parent != nil && node.Parent.Data == "head" && node.FirstChild != nil {
				title = truncatePageText(node.FirstChild.Data, 200)
			}
			if node.Data == "body" {
				body = node
			}
			if hiddenWebPageNode(node) {
				return
			}
			main := node.Data == "article" || node.Data == "main"
			for _, attr := range node.Attr {
				if attr.Key == "role" && attr.Val == "main" {
					main = true
				}
			}
			if main {
				explicit = append(explicit, node)
				return
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child, depth+1)
		}
	}
	visit(document, 0)
	var selected string
	for _, node := range explicit {
		var output strings.Builder
		webPageNodeText(node, 0, &output)
		text := output.String()
		if utf8.RuneCountInString(text) > utf8.RuneCountInString(selected) {
			selected = text
		}
	}
	if len(strings.TrimSpace(selected)) < 24 && body != nil {
		var output strings.Builder
		webPageNodeText(body, 0, &output)
		selected = output.String()
	}
	// 保留段落和表格行，避免温度、日期及表头在清理时被拼成无法对应的单行。
	lines := []string{}
	remaining := webPageMaxRunes
	truncated := false
	for _, line := range strings.Split(selected, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		runes := []rune(line)
		if len(runes) > remaining {
			lines = append(lines, string(runes[:remaining]))
			truncated = true
			break
		}
		lines = append(lines, line)
		remaining -= len(runes) + 1
		if remaining <= 0 {
			truncated = true
			break
		}
	}
	if truncated {
		lines = append(lines, "[正文超出读取上限，后续未读取]")
	}
	return title, strings.Join(lines, "\n"), nil
}
