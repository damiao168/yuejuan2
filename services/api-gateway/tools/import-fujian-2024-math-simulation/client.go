// HTTP 适配保留认证、响应限额和既有超时；非幂等写入不在此层自动重试。
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
	"os"
	"path/filepath"
	"strings"
)

const (
	answerSegmentRoutePrefix = "/api/v1/answer-segments/"
	examRoutePrefix          = "/api/v1/exams/"
	submissionRoutePrefix    = "/api/v1/submissions/"
	jsonContentType          = "application/json"
)

type apiClient struct {
	baseURL       string
	token         string
	loginResponse map[string]any
	client        *http.Client
}

func (c *apiClient) login(ctx context.Context, tenantCode, username, password string) error {
	response, err := c.json(ctx, http.MethodPost, "/api/v1/auth/token", map[string]any{
		"tenant_code": tenantCode,
		"username":    username,
		"password":    password,
		"client_type": "desktop",
		"device_name": "Fujian simulation importer",
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
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
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

func (c *apiClient) upload(ctx context.Context, path string, fields map[string]string) (map[string]any, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return c.uploadBytes(ctx, filepath.Base(path), content, fields)
}

func (c *apiClient) uploadBytes(ctx context.Context, filename string, content []byte, fields map[string]string) (map[string]any, error) {
	if strings.TrimSpace(filename) == "" {
		return nil, errors.New("upload filename is required")
	}
	buffer, writer, err := makeUploadForm(filename, content, fields)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/files", buffer)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", jsonContentType)
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("upload %s returned %d: %s", filepath.Base(filename), response.StatusCode, strings.TrimSpace(string(raw)))
	}
	result := map[string]any{}
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	asset := nestedMap(result, "file")
	if stringField(asset, "id") == "" || stringField(asset, "hash_sha256") == "" {
		return nil, errors.New("upload response lacks file identity/hash")
	}
	return asset, nil
}

func makeUploadForm(filename string, content []byte, fields map[string]string) (*bytes.Buffer, *multipart.Writer, error) {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for key, value := range fields {
		if value != "" {
			if err := writer.WriteField(key, value); err != nil {
				return nil, nil, err
			}
		}
	}
	part, err := writer.CreateFormFile("file", filepath.Base(filename))
	if err != nil {
		return nil, nil, err
	}
	if _, err = part.Write(content); err != nil {
		return nil, nil, err
	}
	if err = writer.Close(); err != nil {
		return nil, nil, err
	}
	return &buffer, writer, nil
}
