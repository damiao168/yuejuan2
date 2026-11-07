package capture

type StudentCandidate struct {
	ID        string `json:"id"`
	StudentNo string `json:"student_no"`
	Name      string `json:"name"`
	ClassID   string `json:"class_id"`
	ClassName string `json:"class_name"`
}

type MatchingSubmission struct {
	ID               string         `json:"id"`
	StudentID        string         `json:"student_id,omitempty"`
	CandidateNo      string         `json:"candidate_no,omitempty"`
	IdentityStatus   string         `json:"identity_status"`
	IdentityRevision int            `json:"identity_revision"`
	IdentityEvidence map[string]any `json:"identity_evidence"`
	Pages            []Page         `json:"pages"`
}

type MatchingQueue struct {
	BatchID     string               `json:"batch_id"`
	ExamID      string               `json:"exam_id"`
	Submissions []MatchingSubmission `json:"submissions"`
	Candidates  []StudentCandidate   `json:"candidates"`
}

type ConfirmStudentMatchInput struct {
	StudentID string `json:"student_id"`
	Revision  int    `json:"revision"`
	Reason    string `json:"reason"`
}

type MarkStudentUnknownInput struct {
	Revision int    `json:"revision"`
	Reason   string `json:"reason"`
}

type ConfirmPageMatchInput struct {
	Revision int    `json:"revision"`
	PageNo   int    `json:"page_no"`
	Reason   string `json:"reason"`
}

type PageLifecycleInput struct {
	Revision int    `json:"revision"`
	Reason   string `json:"reason"`
}

type SplitSubmissionInput struct {
	SubmissionID string   `json:"submission_id"`
	PageIDs      []string `json:"page_ids"`
	Reason       string   `json:"reason"`
}

type MergeSubmissionsInput struct {
	TargetSubmissionID string `json:"target_submission_id"`
	SourceSubmissionID string `json:"source_submission_id"`
	Reason             string `json:"reason"`
}

type UpdatePageInput struct {
	Revision        int  `json:"revision"`
	RotationDegrees *int `json:"rotation_degrees,omitempty"`
	SequenceNo      *int `json:"sequence_no,omitempty"`
}
