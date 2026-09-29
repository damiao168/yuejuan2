// Package regraderelease is the narrow A19→A18 hand-off. It turns only a
// ready, reviewed regrade ReleasePlan into a new draft release; it never
// mutates final_grade or the already published source version.
package regraderelease

import (
	"context"
	"errors"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/regrade"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
)

var (
	ErrNotReady = errors.New("regrade is not ready for a successor release")
	ErrInvalid  = errors.New("invalid regrade release input")
)

type Input struct {
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"idempotency_key"`
}

type Service struct {
	regrades *regrade.Service
	releases *scorerelease.Service
}

func NewService(regrades *regrade.Service, releases *scorerelease.Service) *Service {
	return &Service{regrades: regrades, releases: releases}
}

func (s *Service) Create(ctx context.Context, tenantID, jobID, actorID string, input Input) (scorerelease.Release, error) {
	input.Reason, input.IdempotencyKey = strings.TrimSpace(input.Reason), strings.TrimSpace(input.IdempotencyKey)
	if s == nil || s.regrades == nil || s.releases == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(jobID) == "" || strings.TrimSpace(actorID) == "" || input.Reason == "" {
		return scorerelease.Release{}, ErrInvalid
	}
	summary, err := s.regrades.Get(ctx, tenantID, jobID)
	if err != nil {
		return scorerelease.Release{}, err
	}
	// 至少有一项通过复核并带有复核分数，才创建后继草稿；复核分数可以与原分数相同。
	if summary.ReleasePlan == nil || summary.Job.Status != regrade.StatusReadyForRelease || summary.ReleasePlan.AffectedCount == 0 {
		return scorerelease.Release{}, ErrNotReady
	}
	changes := make([]scorerelease.RegradeChange, 0, len(summary.ReleasePlan.ResolvedItems))
	for _, change := range summary.ReleasePlan.ResolvedItems {
		changes = append(changes, scorerelease.RegradeChange{
			SubmissionID: change.SubmissionID, QuestionID: summary.ReleasePlan.QuestionID,
			Score: change.RegradedScore, MaxScore: change.MaxScore, ReviewedGradeID: change.ReviewedGradeID,
		})
	}
	return s.releases.CreateFromRegrade(ctx, tenantID, summary.ReleasePlan.ExamID, actorID, scorerelease.CreateRegradeInput{
		SourceReleaseID: summary.ReleasePlan.SourceReleaseID, QuestionID: summary.ReleasePlan.QuestionID,
		Reason: input.Reason, IdempotencyKey: input.IdempotencyKey, Changes: changes,
	})
}
