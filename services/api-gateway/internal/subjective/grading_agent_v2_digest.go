package subjective

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
)

func computeGradingAgentV2BindingHash(
	requestID string,
	questionID string,
	segmentID string,
	mediaSHA256 string,
	bbox ActiveCropBBox,
) string {
	parts := []string{
		gradingAgentV2BuilderSchemaVersion,
		requestID,
		questionID,
		segmentID,
		mediaSHA256,
		fmt.Sprintf("%.0f", math.Round(bbox.X*activeCropBBoxScale)),
		fmt.Sprintf("%.0f", math.Round(bbox.Y*activeCropBBoxScale)),
		fmt.Sprintf("%.0f", math.Round(bbox.Width*activeCropBBoxScale)),
		fmt.Sprintf("%.0f", math.Round(bbox.Height*activeCropBBoxScale)),
	}
	hash := sha256.New()
	for _, part := range parts {
		_, _ = fmt.Fprintf(hash, "%d:%s", len([]byte(part)), part)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func gradingAgentV2IdempotencyDigest(request gradingAgentV2Request) (string, error) {
	media := request.MediaEvidence
	safe := gradingAgentV2DigestRequest{
		SchemaVersion:    request.SchemaVersion,
		RequestID:        request.RequestID,
		Subject:          request.Subject,
		GradeLevel:       request.GradeLevel,
		AgentRole:        request.AgentRole,
		QuestionID:       request.QuestionID,
		AnswerSegmentID:  request.AnswerSegmentID,
		QuestionType:     request.QuestionType,
		QuestionText:     request.QuestionText,
		MaxScore:         request.MaxScore,
		AnswerText:       request.AnswerText,
		OCRConfidence:    request.OCRConfidence,
		RubricVersion:    request.RubricVersion,
		PromptVersion:    request.PromptVersion,
		Rubric:           request.Rubric,
		ModelPolicy:      request.ModelPolicy,
		PromptGuard:      request.PromptGuard,
		OutputConstraint: request.OutputConstraint,
		MediaEvidence: gradingAgentV2DigestEvidence{
			Kind:           media.Kind,
			Encoding:       media.Encoding,
			MediaType:      media.MediaType,
			SHA256:         media.SHA256,
			ByteSize:       media.ByteSize,
			WidthPixels:    media.WidthPixels,
			HeightPixels:   media.HeightPixels,
			NormalizedBBox: media.NormalizedBBox,
			BindingHash:    media.BindingHash,
		},
		MathEvidence:       request.MathEvidence,
		ReferenceContext:   request.ReferenceContext,
		RequiredFeatures:   request.RequiredFeatures,
		ReferenceManifest:  request.ReferenceManifest,
		RequestBindingHash: request.RequestBindingHash,
	}
	for _, item := range request.ReferenceMedia {
		item.DataBase64 = ""
		safe.ReferenceMedia = append(safe.ReferenceMedia, item)
	}
	encoded, err := json.Marshal(safe)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	clear(encoded)
	return hex.EncodeToString(digest[:]), nil
}
