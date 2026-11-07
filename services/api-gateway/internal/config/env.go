package config

import (
	"bufio"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("invalid env line: %q", line)
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func getEnvOrFile(key, fallback string) (string, error) {
	if value, ok := os.LookupEnv(key); ok {
		if filePath := strings.TrimSpace(os.Getenv(key + "_FILE")); filePath != "" {
			if strings.TrimSpace(value) != "" {
				return "", fmt.Errorf("%s and %s_FILE cannot both be set", key, key)
			}
			content, err := os.ReadFile(filePath)
			if err != nil {
				return "", fmt.Errorf("read %s_FILE: %w", key, err)
			}
			return strings.TrimRight(string(content), "\r\n"), nil
		}
		return value, nil
	}
	filePath := strings.TrimSpace(os.Getenv(key + "_FILE"))
	if filePath == "" {
		return fallback, nil
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read %s_FILE: %w", key, err)
	}
	return strings.TrimRight(string(content), "\r\n"), nil
}

type envParser struct {
	strict   bool
	problems []string
}

func (p *envParser) raw(key string) (string, bool) {
	value, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(value) == "" {
		return "", false
	}
	return strings.TrimSpace(value), true
}

func (p *envParser) invalid(key, expected string) {
	if p.strict {
		p.problems = append(p.problems, fmt.Sprintf("%s must be a valid %s", key, expected))
		return
	}
	// 不要记录原始值：配置可能来自共享密钥文件。
	slog.Warn("invalid configuration value; using development default", "key", key, "expected", expected)
}

func (p *envParser) Int(key string, fallback int) int {
	value, ok := p.raw(key)
	if !ok {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		p.invalid(key, "integer")
		return fallback
	}
	return parsed
}

func (p *envParser) Bool(key string, fallback bool) bool {
	value, ok := p.raw(key)
	if !ok {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		p.invalid(key, "boolean")
		return fallback
	}
	return parsed
}

func (p *envParser) Duration(key string, fallback time.Duration) time.Duration {
	value, ok := p.raw(key)
	if !ok {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		p.invalid(key, "duration")
		return fallback
	}
	return parsed
}

func (p *envParser) Float(key string, fallback float64) float64 {
	value, ok := p.raw(key)
	if !ok {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		p.invalid(key, "finite number")
		return fallback
	}
	return parsed
}

func (p *envParser) Err() error {
	if len(p.problems) == 0 {
		return nil
	}
	return fmt.Errorf("invalid production configuration: %s", strings.Join(p.problems, "; "))
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
