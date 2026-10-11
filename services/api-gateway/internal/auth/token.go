package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// NewToken 返回原始令牌和存储用哈希；原值交给客户端，数据库只保存第二个返回值。
func NewToken() (string, string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	return token, HashToken(token), nil
}

// HashToken 为高熵随机令牌和恢复码生成查找摘要；用户密码必须走带盐的 HashPassword。
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
