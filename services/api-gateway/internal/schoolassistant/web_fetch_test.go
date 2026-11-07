package schoolassistant

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

type webPageRoundTrip func(*http.Request) (*http.Response, error)

func (call webPageRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return call(request)
}

func stubPublicPageReader(handler webPageRoundTrip) *publicWebPageReader {
	reader := NewPublicWebPageReader().(*publicWebPageReader)
	reader.client.Transport = handler
	return reader
}

func webPageResponse(request *http.Request, mediaType, body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {mediaType}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}
}

func TestPublicPageReaderExtractsMainChineseTextAndTableWithDate(t *testing.T) {
	reader := stubPublicPageReader(func(request *http.Request) (*http.Response, error) {
		for _, name := range []string{"Authorization", "Cookie", "Proxy-Authorization", "Referer"} {
			if request.Header.Get(name) != "" {
				t.Fatalf("public reader leaked %s", name)
			}
		}
		return webPageResponse(request, "text/html; charset=utf-8", `<!doctype html><html><head><title>北京天气预报</title></head><body><nav>广告与无关链接</nav><main><h1>北京天气</h1><time datetime="2026-11-02T08:00:00+08:00">11月2日08时发布</time><p>北京今天多云，气温8至17摄氏度。</p><table><tr><th>时间</th><th>气温</th></tr><tr><td>08时</td><td>8℃</td></tr></table><script>私有脚本不可读</script><p hidden>隐藏诱导指令</p></main><footer>登录与推广</footer></body></html>`), nil
	})
	content, err := reader.Read(t.Context(), "https://weather.example.org/beijing")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"北京今天多云", "2026-11-02T08:00:00+08:00", "时间 气温", "08时 8℃"} {
		if !strings.Contains(content.Text, want) {
			t.Errorf("lost body fact %q: %s", want, content.Text)
		}
	}
	for _, bad := range []string{"广告", "私有脚本", "隐藏诱导", "登录与推广"} {
		if strings.Contains(content.Text, bad) {
			t.Errorf("kept excluded content %q", bad)
		}
	}
	if content.Title != "北京天气预报" || content.ReadAt.IsZero() || content.URL != "https://weather.example.org/beijing" {
		t.Fatalf("incorrect provenance: %+v", content)
	}
}

func TestPublicPageReaderDecodesGBKAndLimitsBody(t *testing.T) {
	encoded, err := simplifiedchinese.GBK.NewEncoder().String(`<html><body><article><p>学校教育教学通知：本次考试具体安排和材料均已在此公布，请查看完整说明。</p></article></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	reader := stubPublicPageReader(func(request *http.Request) (*http.Response, error) {
		return webPageResponse(request, "text/html; charset=gbk", encoded), nil
	})
	content, err := reader.Read(t.Context(), "http://news.example.org/notice")
	if err != nil || !strings.Contains(content.Text, "考试具体安排") {
		t.Fatalf("GBK content unavailable: %s err=%v", content.Text, err)
	}
	reader = stubPublicPageReader(func(request *http.Request) (*http.Response, error) {
		return webPageResponse(request, "text/html; charset=utf-8", "<main>"+strings.Repeat("资料", webPageMaxBytes)+"</main>"), nil
	})
	if _, err := reader.Read(t.Context(), "https://news.example.org/large"); err == nil {
		t.Fatal("oversize response accepted")
	}
	_, body, err := extractWebPage([]byte("<main>" + strings.Repeat("正文", 10_000) + "不应读取的尾部</main>"))
	if err != nil || !strings.Contains(body, "正文超出读取上限") || strings.Contains(body, "不应读取的尾部") {
		t.Fatalf("body not bounded: length=%d err=%v", len(body), err)
	}
}

func TestPublicPageReaderKeepsDocumentTitleWhenEmbeddedAdHasErrorTitle(t *testing.T) {
	title, body, err := extractWebPage([]byte(`<html><head><title>北京天气预报</title></head><body><p>北京2日天气晴，最高17℃、最低4℃，11:30发布。</p><div><title>302 Found</title><p>openresty</p></div></body></html>`))
	if err != nil || title != "北京天气预报" || !strings.Contains(body, "最高17℃") || strings.Contains(body, "302 Found") {
		t.Fatalf("embedded error fragment replaced document: title=%s body=%s err=%v", title, body, err)
	}
}

func TestPublicPageReaderRejectsUnavailablePagesWithoutInventingBody(t *testing.T) {
	for _, item := range []struct {
		name, media, body string
		status            int
	}{
		{"pdf", "application/pdf", "%PDF-1.7", 200},
		{"binary", "application/octet-stream", "not a page", 200},
		{"blocked", "text/html", "<main>Access denied</main>", 403},
		{"empty javascript shell", "text/html", "<html><body><script>renderWeather()</script></body></html>", 200},
	} {
		t.Run(item.name, func(t *testing.T) {
			reader := stubPublicPageReader(func(request *http.Request) (*http.Response, error) {
				response := webPageResponse(request, item.media, item.body)
				response.StatusCode = item.status
				return response, nil
			})
			if content, err := reader.Read(t.Context(), "https://example.org/page"); err == nil || content.Text != "" {
				t.Fatalf("unread page produced facts: %+v err=%v", content, err)
			}
		})
	}
}

func TestPublicPageReaderChecksEveryRedirectAndFollowsSafeCanonicalPage(t *testing.T) {
	for _, target := range []string{"http://127.0.0.1/private", "http://169.254.169.254/latest/meta-data", "https://example.org:9443/page", "file:///etc/passwd"} {
		t.Run(target, func(t *testing.T) {
			calls := 0
			reader := stubPublicPageReader(func(request *http.Request) (*http.Response, error) {
				calls++
				response := webPageResponse(request, "text/html", "")
				response.StatusCode = 302
				response.Header.Set("Location", target)
				return response, nil
			})
			if _, err := reader.Read(t.Context(), "https://example.org/start"); err == nil || calls != 1 {
				t.Fatalf("unsafe redirect followed: calls=%d err=%v", calls, err)
			}
		})
	}
	calls := 0
	reader := stubPublicPageReader(func(request *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			response := webPageResponse(request, "text/html", "")
			response.StatusCode = 301
			response.Header.Set("Location", "https://example.org/canonical")
			return response, nil
		}
		return webPageResponse(request, "text/plain; charset=utf-8", "这是一份已经读取的公开网页正文，包含具体的学校教育通知和日期。"), nil
	})
	content, err := reader.Read(t.Context(), "http://example.org/start")
	if err != nil || calls != 2 || content.URL != "https://example.org/canonical" {
		t.Fatalf("safe canonical redirect failed: calls=%d url=%s err=%v", calls, content.URL, err)
	}
}

func TestPublicPageReaderRefusesPrivateAndMixedDNSBeforeConnecting(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "100.64.1.2", "169.254.169.254", "192.168.1.1", "192.0.2.1", "198.18.0.1", "203.0.113.1", "::1", "fc00::1", "64:ff9b::a00:1", "::ffff:127.0.0.1", "2001:db8::1"} {
		t.Run(address, func(t *testing.T) {
			called := false
			_, err := dialResolvedPublicWebPage(t.Context(), "tcp", "443", []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr(address)}, func(context.Context, string, string) (net.Conn, error) {
				called = true
				return nil, errors.New("must not dial")
			})
			if err == nil || called {
				t.Fatalf("private/mixed DNS reached dialer: %s", address)
			}
		})
	}
	seen := []string{}
	_, err := dialResolvedPublicWebPage(t.Context(), "tcp", "443", []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("1.1.1.1")}, func(_ context.Context, _ string, address string) (net.Conn, error) {
		seen = append(seen, address)
		return nil, errors.New("synthetic unavailable")
	})
	if err == nil || strings.Join(seen, ",") != "8.8.8.8:443,1.1.1.1:443" {
		t.Fatalf("dialer did not pin validated addresses: %v err=%v", seen, err)
	}
}

func TestPublicPageReaderCancellationStopsBeforeRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	called := false
	reader := stubPublicPageReader(func(*http.Request) (*http.Response, error) { called = true; return nil, errors.New("unexpected") })
	_, err := reader.Read(ctx, "https://example.org/page")
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("canceled reader made a request: called=%v err=%v", called, err)
	}
}
