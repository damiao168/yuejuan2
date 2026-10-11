package minioadapter

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/config"
)

func TestNewClientUsesConfiguredCAForTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Query().Has("location") {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">us-east-1</LocationConstraint>`))
			return
		}
		if r.Method == http.MethodHead && r.URL.Path == "/edugrade-files/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		t.Fatalf("unexpected MinIO TLS request: %s %s", r.Method, r.URL.String())
	}))
	defer server.Close()

	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	caFile := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(caFile, certificate, 0o600); err != nil {
		t.Fatal(err)
	}
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(config.MinIOConfig{
		Endpoint:  endpoint.Host,
		AccessKey: "test-app-access",
		SecretKey: "test-app-secret",
		UseSSL:    true,
		TLSCAFile: caFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	exists, err := client.BucketExists(context.Background(), "edugrade-files")
	if err != nil || !exists {
		t.Fatalf("custom CA must verify the MinIO endpoint: exists=%v err=%v", exists, err)
	}
}

func TestNewClientRejectsInvalidCAFile(t *testing.T) {
	caFile := filepath.Join(t.TempDir(), "invalid-ca.crt")
	if err := os.WriteFile(caFile, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewClient(config.MinIOConfig{Endpoint: "minio:9000", UseSSL: true, TLSCAFile: caFile})
	if err == nil {
		t.Fatal("invalid MinIO CA must be rejected")
	}
}

func TestNewClientRejectsTLSBeforeVersion12(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("TLS 1.1 endpoint must be rejected before sending an HTTP request")
	}))
	// 故意只提供旧协议，验证生产客户端在发送 HTTP 前拒绝它；这不是客户端的 TLS 配置。
	server.TLS = &tls.Config{MaxVersion: tls.VersionTLS11}
	server.StartTLS()
	defer server.Close()

	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	caFile := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(caFile, certificate, 0o600); err != nil {
		t.Fatal(err)
	}
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(config.MinIOConfig{
		Endpoint:  endpoint.Host,
		AccessKey: "test-app-access",
		SecretKey: "test-app-secret",
		UseSSL:    true,
		TLSCAFile: caFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.BucketExists(context.Background(), "edugrade-files"); err == nil {
		t.Fatal("TLS versions below 1.2 must be rejected")
	}
}
