package modelgovernance

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

var ErrInvalidSecretReference = errors.New("invalid secret reference")

type SecretProbe struct {
	Scheme               string `json:"scheme"`
	ResolverSupported    bool   `json:"resolver_supported"`
	Configured           bool   `json:"configured"`
	MeetsMinimumStrength bool   `json:"meets_minimum_strength"`
}

type SecretReferenceResolver interface {
	Probe(reference string) (SecretProbe, error)
}

type EnvironmentSecretResolver struct {
	dockerSecretDir string
}

func NewEnvironmentSecretResolver(dockerSecretDir string) *EnvironmentSecretResolver {
	if strings.TrimSpace(dockerSecretDir) == "" {
		dockerSecretDir = "/run/secrets"
	}
	return &EnvironmentSecretResolver{dockerSecretDir: dockerSecretDir}
}

// 已登记的下划线协议不是标准 URL scheme；仅在解析时替换协议，不能改写持久化引用格式。
// net/url 仍负责 authority 和转义检查，本地引用与云层级路径使用各自原有边界。
func parseCredentialReference(reference string) (*url.URL, error) {
	scheme, rest, found := strings.Cut(strings.TrimSpace(reference), "://")
	scheme = strings.ToLower(scheme)
	if !found {
		return nil, ErrInvalidSecretReference
	}
	switch scheme {
	case "env", "docker_secret", "vault", "aws_secrets_manager", "azure_key_vault", "gcp_secret_manager":
	default:
		return nil, ErrInvalidSecretReference
	}
	parsed, err := url.Parse("secret://" + rest)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(rest, "#") {
		return nil, ErrInvalidSecretReference
	}
	parsed.Scheme = scheme
	name := strings.TrimPrefix(parsed.Host+parsed.Path, "/")
	if name == "" || strings.Contains(name, "..") {
		return nil, ErrInvalidSecretReference
	}
	if (scheme == "env" || scheme == "docker_secret") && strings.ContainsAny(name, `/\`) {
		return nil, ErrInvalidSecretReference
	}
	return parsed, nil
}

// Probe 只返回配置与长度检查结果，不返回密钥内容；env 读取当前值，docker_secret 只检查文件元数据。
// 本地引用只接受单个名称；云引用保留层级路径，所有引用拒绝用户信息、参数、片段和穿越。
// 未安装解析器的外部密钥服务返回不支持，不能由引用语法合法推断为可激活。
func (r *EnvironmentSecretResolver) Probe(reference string) (SecretProbe, error) {
	parsed, err := parseCredentialReference(reference)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return SecretProbe{}, ErrInvalidSecretReference
	}
	name := strings.TrimPrefix(parsed.Host+parsed.Path, "/")
	if name == "" || strings.Contains(name, "..") {
		return SecretProbe{}, ErrInvalidSecretReference
	}
	switch parsed.Scheme {
	case "env":
		if strings.ContainsAny(name, `/\`) {
			return SecretProbe{}, ErrInvalidSecretReference
		}
		value, exists := os.LookupEnv(name)
		configured := exists && value != ""
		return SecretProbe{
			Scheme:               parsed.Scheme,
			ResolverSupported:    true,
			Configured:           configured,
			MeetsMinimumStrength: configured && len(value) >= 24,
		}, nil
	case "docker_secret":
		if strings.ContainsAny(name, `/\`) {
			return SecretProbe{}, ErrInvalidSecretReference
		}
		info, statErr := os.Stat(filepath.Join(r.dockerSecretDir, name))
		return SecretProbe{
			Scheme:               parsed.Scheme,
			ResolverSupported:    true,
			Configured:           statErr == nil && !info.IsDir() && info.Size() > 0,
			MeetsMinimumStrength: statErr == nil && !info.IsDir() && info.Size() >= 24,
		}, nil
	case "vault", "aws_secrets_manager", "azure_key_vault", "gcp_secret_manager":
		return SecretProbe{Scheme: parsed.Scheme, ResolverSupported: false, Configured: false, MeetsMinimumStrength: false}, nil
	default:
		return SecretProbe{}, ErrInvalidSecretReference
	}
}
