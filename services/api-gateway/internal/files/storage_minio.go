package files

import (
	"context"
	"errors"
	"fmt"
	"io"

	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/minioadapter"
	"github.com/minio/minio-go/v7"
)

type MinIOObjectStorage struct {
	client *minio.Client
}

func NewMinIOObjectStorage(cfg config.MinIOConfig) (*MinIOObjectStorage, error) {
	client, err := minioadapter.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &MinIOObjectStorage{client: client}, nil
}

func (s *MinIOObjectStorage) Put(ctx context.Context, bucket string, key string, body io.Reader, size int64, contentType string) error {
	// 桶由部署流程准备；缺桶作为配置故障返回，不在上传请求中自动创建。
	exists, err := s.client.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: %s", ErrBucketMissing, bucket)
	}
	_, err = s.client.PutObject(ctx, bucket, key, body, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

func (s *MinIOObjectStorage) Get(ctx context.Context, bucket string, key string) (io.ReadCloser, error) {
	// GetObject 延迟读取；下面的 Stat 提前暴露不存在或无权访问的对象。
	object, err := s.client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	if _, err := object.Stat(); err != nil {
		_ = object.Close()
		return nil, err
	}
	return object, nil
}

func (s *MinIOObjectStorage) Remove(ctx context.Context, bucket string, key string) error {
	return s.client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{})
}

func (s *MinIOObjectStorage) Stat(ctx context.Context, bucket string, key string) (ObjectInfo, error) {
	info, err := s.client.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err != nil {
		response := minio.ToErrorResponse(err)
		if response.Code == "NoSuchKey" || response.Code == "NoSuchObject" || response.Code == "NotFound" {
			return ObjectInfo{}, ErrObjectNotFound
		}
		return ObjectInfo{}, err
	}
	return ObjectInfo{Bucket: bucket, Key: key, SizeBytes: info.Size, LastModified: info.LastModified}, nil
}

func (s *MinIOObjectStorage) List(ctx context.Context, bucket string, prefix string, limit int) ([]ObjectInfo, error) {
	if limit <= 0 || limit > 10000 {
		limit = 1000
	}
	objects := make([]ObjectInfo, 0, limit)
	for item := range s.client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if item.Err != nil {
			if errors.Is(item.Err, context.Canceled) {
				return nil, item.Err
			}
			return nil, item.Err
		}
		objects = append(objects, ObjectInfo{Bucket: bucket, Key: item.Key, SizeBytes: item.Size, LastModified: item.LastModified})
		if len(objects) >= limit {
			break
		}
	}
	return objects, nil
}
