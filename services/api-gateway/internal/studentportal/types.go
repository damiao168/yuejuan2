// Package studentportal 只管理面向学生的发现功能。
// 成绩事实和题目反馈仍归属于 scorerelease 中不可变且面向学生的受限 DTO，
// 避免本包意外引入可变或管理员形式的结果表示。
package studentportal

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidInput = errors.New("invalid student portal input")
	ErrNotFound     = errors.New("student portal resource not found")
)

// PublishedExam 只包含学生选择已发布结果所需的元数据。
// 它省略发布标识、同学、排名、质量信号和全部评分来源信息。
type PublishedExam struct {
	ExamID         string    `json:"exam_id"`
	Name           string    `json:"name"`
	Subject        string    `json:"subject"`
	ExamType       string    `json:"exam_type,omitempty"`
	ReleaseVersion int       `json:"release_version"`
	PublishedAt    time.Time `json:"published_at"`
	TotalScore     float64   `json:"total_score,omitempty"`
	MaxScore       float64   `json:"max_score,omitempty"`
}

type Store interface {
	ListPublishedExams(context.Context, string, string) ([]PublishedExam, error)
}
