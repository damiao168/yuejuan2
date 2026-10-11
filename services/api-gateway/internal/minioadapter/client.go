package minioadapter

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/config"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// NewClient 集中管理 MinIO TLS 验证，防止就绪检查和业务 I/O 静默使用不同信任设置。
func NewClient(cfg config.MinIOConfig) (*minio.Client, error) {
	options := &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	}
	if cfg.UseSSL {
		transport, err := minio.DefaultTransport(true)
		if err != nil {
			return nil, fmt.Errorf("create MinIO TLS transport: %w", err)
		}
		tlsConfig := transport.TLSClientConfig.Clone()
		tlsConfig.MinVersion = tls.VersionTLS12
		if caFile := strings.TrimSpace(cfg.TLSCAFile); caFile != "" {
			pemData, err := os.ReadFile(caFile)
			if err != nil {
				return nil, fmt.Errorf("read EDUGRADE_MINIO_TLS_CA_FILE: %w", err)
			}
			rootCAs, err := x509.SystemCertPool()
			if err != nil || rootCAs == nil {
				rootCAs = x509.NewCertPool()
			}
			if ok := rootCAs.AppendCertsFromPEM(pemData); !ok {
				return nil, fmt.Errorf("EDUGRADE_MINIO_TLS_CA_FILE contains no valid PEM certificates")
			}
			tlsConfig.RootCAs = rootCAs
		}
		if serverName := strings.TrimSpace(cfg.TLSServerName); serverName != "" {
			tlsConfig.ServerName = serverName
		}
		transport.TLSClientConfig = tlsConfig
		options.Transport = transport
	}
	client, err := minio.New(cfg.Endpoint, options)
	if err != nil {
		return nil, fmt.Errorf("create MinIO client: %w", err)
	}
	return client, nil
}
