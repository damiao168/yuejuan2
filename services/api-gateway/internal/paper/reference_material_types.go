package paper

import (
	"time"
)

type ReferencePixelBox struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type ReferenceMaterialSource struct {
	PageRef       string `json:"page_ref"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	PageNo        int    `json:"page_no"`
	SHA256        string `json:"sha256"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	SourceAssetID string `json:"-"`
	PageAssetID   string `json:"-"`
	SourceHash    string `json:"source_sha256"`
	Identity      string `json:"source_version"`
}

type ReferenceMaterialItem struct {
	ID          string                  `json:"id"`
	CropRef     string                  `json:"crop_ref"`
	Role        string                  `json:"role"`
	Ordinal     int                     `json:"ordinal"`
	Required    bool                    `json:"required"`
	SHA256      string                  `json:"sha256"`
	Width       int                     `json:"width"`
	Height      int                     `json:"height"`
	SizeBytes   int64                   `json:"size_bytes"`
	Box         ReferencePixelBox       `json:"pixel_bbox"`
	Source      ReferenceMaterialSource `json:"source"`
	FileAssetID string                  `json:"-"`
}

type ReferenceMaterials struct {
	ID                    string                  `json:"manifest_id,omitempty"`
	Mode                  string                  `json:"mode"`
	Revision              int                     `json:"revision"`
	ContentHash           string                  `json:"content_hash,omitempty"`
	AuthorizationRevision int                     `json:"authorization_revision,omitempty"`
	UseState              string                  `json:"use_state,omitempty"`
	Frozen                bool                    `json:"frozen"`
	Items                 []ReferenceMaterialItem `json:"items"`
	CreatedBy             string                  `json:"created_by,omitempty"`
	CreatedAt             time.Time               `json:"created_at,omitempty"`
}

// ReferenceMaterialBinding 只含冻结内容身份；实时撤回状态不能改变历史快照摘要。
type ReferenceMaterialBinding struct {
	ID          string `json:"manifest_id"`
	Mode        string `json:"mode"`
	Revision    int    `json:"revision"`
	ContentHash string `json:"content_hash"`
	ImageCount  int    `json:"image_count"`
}

func referenceMaterialBinding(m ReferenceMaterials) *ReferenceMaterialBinding {
	if m.ID == "" {
		return nil
	}
	return &ReferenceMaterialBinding{m.ID, m.Mode, m.Revision, m.ContentHash, len(m.Items)}
}

type SaveReferenceMaterialsInput struct {
	CommandID        string `json:"command_id"`
	ExpectedRevision int    `json:"expected_revision"`
	Mode             string `json:"mode"`
	Items            []struct {
		CropRef string `json:"crop_ref"`
		Role    string `json:"role"`
		Ordinal int    `json:"ordinal"`
	} `json:"items"`
}

type CreateReferenceCropInput struct {
	CommandID  string            `json:"command_id"`
	PageRef    string            `json:"page_ref"`
	PageSHA256 string            `json:"page_sha256"`
	Width      int               `json:"page_width"`
	Height     int               `json:"page_height"`
	Box        ReferencePixelBox `json:"pixel_bbox"`
}

type WithdrawReferenceMaterialsInput struct {
	CommandID                     string `json:"command_id"`
	ExpectedAuthorizationRevision int    `json:"expected_authorization_revision"`
	Reason                        string `json:"reason"`
}
