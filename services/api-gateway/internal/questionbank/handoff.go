package questionbank

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"time"
)

type HandoffState struct {
	Revision    int64      `json:"revision"`
	ReviewerID  string     `json:"reviewer_id,omitempty"`
	PublisherID string     `json:"publisher_id,omitempty"`
	ClaimedBy   string     `json:"claimed_by,omitempty"`
	ClaimedAt   *time.Time `json:"claimed_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
}
type HandoffInput struct {
	ExpectedRevision        int64  `json:"expected_revision"`
	ExpectedHandoffRevision int64  `json:"expected_handoff_revision"`
	ReviewerID              string `json:"reviewer_id,omitempty"`
	PublisherID             string `json:"publisher_id,omitempty"`
	// Manage 只由认证处理器按当前职责填入，不接受浏览器指定管理身份。
	Manage     bool `json:"-"`
	AuthorEdit bool `json:"-"`
}
type HandoffEvent struct {
	ID              string    `json:"id"`
	VersionID       string    `json:"version_id"`
	HandoffRevision int64     `json:"handoff_revision"`
	ContentRevision int64     `json:"content_revision"`
	Kind            string    `json:"kind"`
	ActorID         string    `json:"actor_id"`
	RecipientID     string    `json:"recipient_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

func handoffRevision(v Version) int64 {
	if v.Handoff == nil {
		return 0
	}
	return v.Handoff.Revision
}
func handoffRecipient(v Version) string {
	if v.Handoff == nil {
		return ""
	}
	if v.WorkflowStatus == "reviewing" {
		return v.Handoff.ReviewerID
	}
	if v.WorkflowStatus == "approved" {
		return v.Handoff.PublisherID
	}
	return ""
}
func checkHandoffOwner(v Version, actor string, expected int64) error {
	if expected != handoffRevision(v) {
		return ErrConflict
	}
	if assignee := handoffRecipient(v); assignee != "" && assignee != actor {
		return auth.ErrForbidden
	}
	return nil
}
func handoffState(v Version) HandoffState {
	if v.Handoff == nil {
		return HandoffState{}
	}
	return *v.Handoff
}
func handoffAssignment(v Version, in HandoffInput) (HandoffState, error) {
	if v.Revision != in.ExpectedRevision || handoffRevision(v) != in.ExpectedHandoffRevision {
		return HandoffState{}, ErrConflict
	}
	if !oneOf(v.WorkflowStatus, "reviewing", "approved") {
		return HandoffState{}, ErrLocked
	}
	if !validID(in.ReviewerID) || !validID(in.PublisherID) || in.ReviewerID == v.AuthorID {
		return HandoffState{}, ErrInvalidInput
	}
	h := handoffState(v)
	h.Revision++
	h.ReviewerID = in.ReviewerID
	h.PublisherID = in.PublisherID
	h.ClaimedBy = ""
	h.ClaimedAt = nil
	h.UpdatedAt = time.Now().UTC()
	return h, nil
}
func handoffClaim(v Version, actor, action string, in HandoffInput) (HandoffState, error) {
	if in.ExpectedRevision != v.Revision {
		return HandoffState{}, ErrConflict
	}
	if action == "review" && (v.WorkflowStatus != "reviewing" || actor == v.AuthorID) || action == "publish" && v.WorkflowStatus != "approved" || !oneOf(action, "review", "publish") {
		return HandoffState{}, ErrLocked
	}
	if err := checkHandoffOwner(v, actor, in.ExpectedHandoffRevision); err != nil {
		return HandoffState{}, err
	}
	h := handoffState(v)
	if h.ClaimedBy != "" {
		return HandoffState{}, ErrConflict
	}
	h.Revision++
	h.ClaimedBy = actor
	now := time.Now().UTC()
	h.ClaimedAt = &now
	h.UpdatedAt = now
	if action == "review" {
		h.ReviewerID = actor
	} else {
		h.PublisherID = actor
	}
	return h, nil
}

func prepareHandoffTransition(v Version, actor, decision string, in ReviewInput, recipientCheck func(string, string) error) (*HandoffState, string, error) {
	h := handoffState(v)
	assigned := h.ReviewerID != "" || h.PublisherID != ""
	if h.Revision != in.ExpectedHandoffRevision && (assigned || in.ExpectedHandoffRevision != 0) {
		return nil, "", ErrConflict
	}
	if decision == "submit-review" {
		if in.ReviewerID != "" || in.PublisherID != "" {
			h.ReviewerID = in.ReviewerID
			h.PublisherID = in.PublisherID
		}
		if h.ReviewerID != "" || h.PublisherID != "" {
			if !validID(h.ReviewerID) || !validID(h.PublisherID) || h.ReviewerID == v.AuthorID {
				return nil, "", ErrInvalidInput
			}
			if err := recipientCheck(h.ReviewerID, "review"); err != nil {
				return nil, "", err
			}
			if err := recipientCheck(h.PublisherID, "publish"); err != nil {
				return nil, "", err
			}
		}
	} else if (decision != "return-to-draft" || actor != v.AuthorID) && (assigned || in.ExpectedHandoffRevision != 0) {
		ownerVersion := v
		// 退回沿用审核职责；即使已批准，指定审核教师仍可撤回自己的审核，发布人不能代替审核人退回。
		if decision == "return-to-draft" {
			ownerVersion.WorkflowStatus = "reviewing"
		}
		if err := checkHandoffOwner(ownerVersion, actor, in.ExpectedHandoffRevision); err != nil {
			return nil, "", err
		}
	}
	if decision == "approve" && h.PublisherID != "" {
		if err := recipientCheck(h.PublisherID, "publish"); err != nil {
			return nil, "", err
		}
	}
	// 历史未指派版本保留兼容；一旦有正式交接，后续每次转换均推进独立交接修订，旧领取不能沿用。
	h.Revision++
	h.ClaimedBy = ""
	h.ClaimedAt = nil
	h.UpdatedAt = time.Now().UTC()
	recipient := ""
	if decision == "submit-review" {
		recipient = h.ReviewerID
	}
	if decision == "approve" {
		recipient = h.PublisherID
	}
	return &h, recipient, nil
}
