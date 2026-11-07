package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
)

func (c *apiClient) login(ctx context.Context, tenantCode, username, password string) error {
	response, err := c.json(ctx, http.MethodPost, "/api/v1/auth/token", map[string]any{
		"tenant_code": tenantCode, "username": username, "password": password,
		"client_type": "desktop", "device_name": "STORY-056 validation",
	}, http.StatusOK)
	if err != nil {
		return err
	}
	c.token = stringField(response, "access_token")
	if c.token == "" {
		return errors.New("login response did not include access_token")
	}
	return nil
}

func (c *apiClient) currentUser() map[string]any {
	return nestedMap(c.loginResponse, "user")
}

// loginResponse 只保留预置隔离夹具所需的不可变身份值，绝不包含密码。
func (c *apiClient) json(ctx context.Context, method, path string, body any, want int) (map[string]any, error) {
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", jsonContentType)
	if body != nil {
		req.Header.Set("Content-Type", jsonContentType)
	}
	if c.token != "" {
		req.Header.Set("Authorization", bearerTokenPrefix+c.token)
	}
	response, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != want {
		return nil, fmt.Errorf("%s %s returned %d: %s", method, path, response.StatusCode, strings.TrimSpace(string(raw)))
	}
	result := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, fmt.Errorf("decode %s %s response: %w", method, path, err)
		}
	}
	if path == "/api/v1/auth/token" && method == http.MethodPost {
		c.loginResponse = result
	}
	return result, nil
}

func (c *apiClient) uploadPNG(ctx context.Context, filename string, content []byte, fields map[string]string) (map[string]any, error) {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, err
		}
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err = part.Write(content); err != nil {
		return nil, err
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/files", &buffer)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", jsonContentType)
	req.Header.Set("Authorization", bearerTokenPrefix+c.token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("upload %s returned %d: %s", filename, response.StatusCode, strings.TrimSpace(string(raw)))
	}
	result := map[string]any{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	asset := nestedMap(result, "file")
	if stringField(asset, "id") == "" || stringField(asset, "hash_sha256") == "" {
		return nil, errors.New("upload response lacks file identity or hash")
	}
	return asset, nil
}

func (c *apiClient) bytes(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", bearerTokenPrefix+c.token)
	response, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s returned %d", path, response.StatusCode)
	}
	return content, nil
}

func (c *apiClient) expectStatus(ctx context.Context, method, path string, want int) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", bearerTokenPrefix+c.token)
	}
	response, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return err
	}
	if response.StatusCode != want {
		return fmt.Errorf("%s %s returned %d, want %d: %s", method, path, response.StatusCode, want, strings.TrimSpace(string(body)))
	}
	return nil
}

func nestedMap(value map[string]any, key string) map[string]any {
	item, _ := value[key].(map[string]any)
	return item
}

func nestedStringField(value map[string]any, nested, key string) string {
	return stringField(nestedMap(value, nested), key)
}

func stringField(value map[string]any, key string) string {
	item, _ := value[key].(string)
	return item
}

func intField(value map[string]any, key string) int {
	item, _ := value[key].(float64)
	return int(item)
}
