package modelgovernance

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"time"
)

func (p *HTTPManagedAPIProber) client() *http.Client {
	return p.httpClient
}

func newManagedProbeHTTPClient(timeout time.Duration) *http.Client {
	transport := &http.Transport{
		DialContext: safePublicDialContext, TLSHandshakeTimeout: 5 * time.Second,
		ResponseHeaderTimeout: timeout, IdleConnTimeout: 30 * time.Second,
		MaxIdleConns: 32, MaxIdleConnsPerHost: 4,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (p *HTTPManagedAPIProber) doReadOnlyRequest(ctx context.Context, client *http.Client, endpoint string, connection ManagedAPIConnection, result *ManagedAPIProbeResult) (*http.Response, error) {
	return retryManagedReadOnlyRequest(ctx, p.timeout, func(attemptCtx context.Context) (*http.Response, error) {
		return p.doRequest(attemptCtx, client, http.MethodGet, endpoint, connection, nil, result)
	})
}

func retryManagedReadOnlyRequest(ctx context.Context, timeout time.Duration, request func(context.Context) (*http.Response, error)) (*http.Response, error) {
	// 只对幂等的模型列表请求重试一次；生成请求可能消耗 Token，不能在这里重试。
	jitterLimit := min(timeout/10, 100*time.Millisecond)
	pause := min(timeout/10, 150*time.Millisecond)
	if jitterLimit > 0 {
		pause += time.Duration(rand.Int63n(int64(jitterLimit)))
	}
	// 首次请求耗尽预算后只给短暂恢复窗口，避免快速检查的等待时间翻倍。
	totalBudget := min(2*timeout+pause, timeout+4*time.Second)
	boundedCtx, cancel := context.WithTimeout(ctx, totalBudget)
	response, err := request(boundedCtx)
	if err != nil && boundedCtx.Err() == nil && retryableManagedProbeError(err) {
		timer := time.NewTimer(pause)
		defer timer.Stop()
		select {
		case <-boundedCtx.Done():
			cancel()
			return nil, boundedCtx.Err()
		case <-timer.C:
		}
		response, err = request(boundedCtx)
	}
	if err != nil || response == nil || response.Body == nil {
		cancel()
		return response, err
	}
	// HTTP 客户端收到响应头就会返回；预算必须保留到正文关闭，提前取消会中断模型列表读取。
	response.Body = &managedReadOnlyProbeBody{ReadCloser: response.Body, cancel: cancel}
	return response, err
}

type managedReadOnlyProbeBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (body *managedReadOnlyProbeBody) Close() error {
	defer body.cancel()
	return body.ReadCloser.Close()
}

func retryableManagedProbeError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, ErrInvalidManagedConfig) {
		return false
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) {
		return true
	}
	var networkErr *net.OpError
	if errors.As(err, &networkErr) {
		return true
	}
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr)
}

func (p *HTTPManagedAPIProber) doRequest(ctx context.Context, client *http.Client, method, endpoint string, connection ManagedAPIConnection, body []byte, result *ManagedAPIProbeResult) (*http.Response, error) {
	timing := newManagedRequestTiming()
	ctx = context.WithValue(ctx, managedRequestTimingKey{}, timing)
	ctx = httptrace.WithClientTrace(ctx, timing.clientTrace())
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+connection.APIKey)
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if connection.Config.ProviderKey == "anthropic" {
		req.Header.Set("X-Api-Key", connection.APIKey)
		req.Header.Set("Anthropic-Version", "2023-06-01")
	}
	req.Header.Set("User-Agent", "EduGrade-API-Probe/2.0")
	response, err := client.Do(req)
	if result != nil {
		diagnostic, failureStage := timing.snapshot()
		result.ConnectionDiagnostic.Attempts++
		result.ConnectionDiagnostic.DNSMS += diagnostic.DNSMS
		result.ConnectionDiagnostic.ConnectMS += diagnostic.ConnectMS
		result.ConnectionDiagnostic.TLSMS += diagnostic.TLSMS
		result.ConnectionDiagnostic.TTFBMS += diagnostic.TTFBMS
		if err != nil {
			result.ConnectionDiagnostic.FailureStage = failureStage
		} else {
			result.ConnectionDiagnostic.FailureStage = ""
		}
	}
	return response, err
}

func readManagedProbeBody(response *http.Response) ([]byte, error) {
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, managedProbeResponseLimit))
	// 预算耗尽时传输层也可能以正常 EOF 结束；关闭正文会取消预算，必须在关闭前核对请求状态。
	if response.Request != nil {
		if requestErr := response.Request.Context().Err(); requestErr != nil {
			return body, requestErr
		}
	}
	return body, err
}

func bodyManagedProbeFailure(result ManagedAPIProbeResult, err error, stage string) ManagedAPIProbeResult {
	result.ConnectionDiagnostic.FailureStage = "body"
	// 收到响应头后仍可能等待正文超时；网络故障不能作为模型能力不合格的证据。
	var netErr net.Error
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) {
		return networkManagedProbeFailure(result, err, stage)
	}
	return failedManagedProbe(result, "provider_invalid_response", "供应商返回了无法读取的响应", stage)
}

func managedModelsEndpoint(baseURL string) (string, error) {
	return managedEndpoint(baseURL, "models")
}

func managedCompletionEndpoint(baseURL string) (string, error) {
	return managedEndpoint(baseURL, "chat/completions")
}

func managedEndpoint(baseURL, suffix string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", ErrInvalidManagedConfig
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + suffix
	return parsed.String(), nil
}

// DNS 返回的每个地址都必须是公网地址；混合公网和内网结果整体拒绝，防止回退路径绕过 SSRF 防护。
func safePublicDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, ErrInvalidManagedConfig
	}
	timing, _ := ctx.Value(managedRequestTimingKey{}).(*managedRequestTiming)
	timing.beginDNS()
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	timing.endDNS(err)
	if err != nil || len(addresses) == 0 {
		return nil, errorsOrInvalid(err)
	}
	dialer := &net.Dialer{Timeout: 2 * time.Second, KeepAlive: 10 * time.Second}
	return dialPublicProviderAddresses(ctx, network, port, addresses, dialer.DialContext, timing)
}

func dialPublicProviderAddresses(ctx context.Context, network, port string, addresses []net.IPAddr,
	dial func(context.Context, string, string) (net.Conn, error), timing *managedRequestTiming) (net.Conn, error) {
	// 建立任何连接前，先整体拒绝不合格的 DNS 回答；回退不得让公私地址混合的回答成为 SSRF 绕过路径。
	for _, address := range addresses {
		if !publicProviderIP(address.IP) {
			return nil, ErrInvalidManagedConfig
		}
	}
	var lastErr error
	for _, address := range addresses {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		timing.beginConnect()
		connection, err := dial(ctx, network, net.JoinHostPort(address.IP.String(), port))
		timing.endConnect(err)
		if err == nil {
			return connection, nil
		}
		lastErr = err
	}
	return nil, errorsOrInvalid(lastErr)
}

func errorsOrInvalid(err error) error {
	if err != nil {
		return err
	}
	return ErrInvalidManagedConfig
}
