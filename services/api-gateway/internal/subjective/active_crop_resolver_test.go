package subjective

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/segment"
)

const activeCropTestPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y9Zl1sAAAAASUVORK5CYII="

func TestActiveCropResolverReturnsVerifiedCurrentCrop(t *testing.T) {
	fixture := newActiveCropResolverFixture(t)
	resolved, err := fixture.resolver.Resolve(context.Background(), "tenant-1", "segment-1", "question-1")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Kind != "answer_segment_crop" ||
		resolved.MediaType != "image/png" ||
		resolved.SHA256 != fixture.evidence.CropSHA256 ||
		resolved.ByteSize != int64(len(fixture.data)) ||
		resolved.WidthPixels != 1 ||
		resolved.HeightPixels != 1 ||
		!bytes.Equal(resolved.Data, fixture.data) {
		t.Fatalf("unexpected resolved crop: %#v", resolved)
	}
	if fixture.evidenceStore.calls != 2 || fixture.assetStore.calls != 2 || fixture.objects.getCalls != 1 {
		t.Fatalf(
			"resolver must attest before and after object read: evidence=%d assets=%d objects=%d",
			fixture.evidenceStore.calls,
			fixture.assetStore.calls,
			fixture.objects.getCalls,
		)
	}
}

func TestActiveCropResolverAcceptsOnlyTheCurrentlyAppliedCorrectionPreview(t *testing.T) {
	fixture := newActiveCropResolverFixture(t)
	fixture.evidence.CorrectionID = "correction-1"
	fixture.asset.OwnerType = "page_registration_correction_preview"
	fixture.asset.OwnerID = "correction-1"
	fixture.refresh()
	if _, err := fixture.resolver.Resolve(context.Background(), "tenant-1", "segment-1", "question-1"); err != nil {
		t.Fatal(err)
	}

	fixture = newActiveCropResolverFixture(t)
	fixture.evidence.CorrectionID = "correction-current"
	fixture.asset.OwnerType = "page_registration_correction_preview"
	fixture.asset.OwnerID = "correction-old"
	fixture.refresh()
	if _, err := fixture.resolver.Resolve(context.Background(), "tenant-1", "segment-1", "question-1"); err != ErrActiveCropUnavailable {
		t.Fatalf("stale correction preview must fail closed: %v", err)
	}
}

func TestActiveCropResolverScopesEvidenceByTenantSegmentAndQuestion(t *testing.T) {
	for _, input := range []struct {
		tenant   string
		segment  string
		question string
	}{
		{tenant: "tenant-other", segment: "segment-1", question: "question-1"},
		{tenant: "tenant-1", segment: "segment-other", question: "question-1"},
		{tenant: "tenant-1", segment: "segment-1", question: "question-other"},
	} {
		withFixture := newActiveCropResolverFixture(t)
		_, err := withFixture.resolver.Resolve(context.Background(), input.tenant, input.segment, input.question)
		if err != ErrActiveCropUnavailable {
			t.Fatalf("scope mismatch unexpectedly resolved: input=%#v err=%v", input, err)
		}
		if withFixture.assetStore.calls != 0 || withFixture.objects.getCalls != 0 {
			t.Fatalf("scope mismatch reached asset or object storage: input=%#v", input)
		}
	}
}

func TestActiveCropResolverDetectsCorrectionUndoDuringObjectRead(t *testing.T) {
	fixture := newActiveCropResolverFixture(t)
	current := fixture.evidence
	current.CorrectionID = "correction-1"
	fixture.asset.OwnerType = "page_registration_correction_preview"
	fixture.asset.OwnerID = current.CorrectionID
	undone := current
	undone.CorrectionID = ""
	undone.CropFileAssetID = "asset-previous"
	undone.CropSHA256 = strings.Repeat("b", 64)
	fixture.evidenceStore.values = []segment.SegmentEvidence{current, undone}
	fixture.assetStore.values = []files.FileAsset{fixture.asset}

	if _, err := fixture.resolver.Resolve(context.Background(), "tenant-1", "segment-1", "question-1"); err != ErrActiveCropUnavailable {
		t.Fatalf("correction undo race must fail closed: %v", err)
	}
}

func TestActiveCropResolverRejectsReplacedObjectContent(t *testing.T) {
	fixture := newActiveCropResolverFixture(t)
	fixture.data = []byte("not-the-attested-png")
	fixture.asset.SizeBytes = int64(len(fixture.data))
	fixture.refresh()
	if _, err := fixture.resolver.Resolve(context.Background(), "tenant-1", "segment-1", "question-1"); err != ErrActiveCropUnavailable {
		t.Fatalf("replaced object must fail closed: %v", err)
	}
}

func TestActiveCropResolverRejectsInvalidMetadata(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name   string
		mutate func(*segment.SegmentEvidence, *files.FileAsset)
	}{
		{name: "processing incomplete", mutate: func(e *segment.SegmentEvidence, _ *files.FileAsset) { e.ProcessingStatus = "pending" }},
		{name: "registration incomplete", mutate: func(e *segment.SegmentEvidence, _ *files.FileAsset) { e.RegistrationStatus = "failed" }},
		{name: "cross exam", mutate: func(_ *segment.SegmentEvidence, a *files.FileAsset) { a.ExamID = "exam-other" }},
		{name: "cross submission", mutate: func(_ *segment.SegmentEvidence, a *files.FileAsset) { a.SubmissionID = "submission-other" }},
		{name: "wrong owner", mutate: func(_ *segment.SegmentEvidence, a *files.FileAsset) { a.OwnerID = "registration-old" }},
		{name: "public visibility", mutate: func(_ *segment.SegmentEvidence, a *files.FileAsset) { a.Visibility = "tenant" }},
		{name: "wrong mime", mutate: func(_ *segment.SegmentEvidence, a *files.FileAsset) { a.ContentType = "image/jpeg" }},
		{name: "deleted asset", mutate: func(_ *segment.SegmentEvidence, a *files.FileAsset) { a.DeletedAt = &now }},
		{name: "asset hash mismatch", mutate: func(_ *segment.SegmentEvidence, a *files.FileAsset) { a.HashSHA256 = strings.Repeat("0", 64) }},
		{name: "near whole page", mutate: func(e *segment.SegmentEvidence, _ *files.FileAsset) {
			e.NormalizedBBox = map[string]any{"x": 0.0, "y": 0.0, "width": 1.0, "height": 1.0}
		}},
		{name: "bbox unknown field", mutate: func(e *segment.SegmentEvidence, _ *files.FileAsset) {
			e.NormalizedBBox["page"] = 1
		}},
		{name: "oversized metadata", mutate: func(_ *segment.SegmentEvidence, a *files.FileAsset) { a.SizeBytes = maxActiveCropBytes + 1 }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture := newActiveCropResolverFixture(t)
			test.mutate(&fixture.evidence, &fixture.asset)
			fixture.refresh()
			if _, err := fixture.resolver.Resolve(context.Background(), "tenant-1", "segment-1", "question-1"); err != ErrActiveCropUnavailable {
				t.Fatalf("invalid metadata must fail closed: %v", err)
			}
		})
	}
}

func TestActiveCropResolverRejectsOversizedObjectBeforeUnboundedRead(t *testing.T) {
	fixture := newActiveCropResolverFixture(t)
	fixture.asset.SizeBytes = maxActiveCropBytes
	fixture.data = bytes.Repeat([]byte{0}, int(maxActiveCropBytes+1))
	fixture.refresh()
	if _, err := fixture.resolver.Resolve(context.Background(), "tenant-1", "segment-1", "question-1"); err != ErrActiveCropUnavailable {
		t.Fatalf("oversized object must fail closed: %v", err)
	}
}

func TestActiveCropResolverPreservesContextCancellation(t *testing.T) {
	fixture := newActiveCropResolverFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fixture.evidenceStore.err = ctx.Err()
	if _, err := fixture.resolver.Resolve(ctx, "tenant-1", "segment-1", "question-1"); err != context.Canceled {
		t.Fatalf("context cancellation was hidden: %v", err)
	}
}

type activeCropResolverFixture struct {
	data          []byte
	evidence      segment.SegmentEvidence
	asset         files.FileAsset
	evidenceStore *activeCropEvidenceFake
	assetStore    *activeCropAssetFake
	objects       *activeCropObjectFake
	resolver      *ActiveCropResolver
}

func newActiveCropResolverFixture(t *testing.T) *activeCropResolverFixture {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(activeCropTestPNGBase64)
	if err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	fixture := &activeCropResolverFixture{
		data: data,
		evidence: segment.SegmentEvidence{
			SegmentID:          "segment-1",
			SubmissionID:       "submission-1",
			SubmissionPageID:   "page-1",
			ExamID:             "exam-1",
			QuestionID:         "question-1",
			RegistrationRunID:  "registration-1",
			NormalizedBBox:     map[string]any{"x": 0.1, "y": 0.2, "width": 0.4, "height": 0.3},
			CropFileAssetID:    "asset-1",
			CropSHA256:         hash,
			ProcessingStatus:   "completed",
			RegistrationStatus: "completed",
		},
		asset: files.FileAsset{
			ID:            "asset-1",
			TenantID:      "tenant-1",
			ExamID:        "exam-1",
			SubmissionID:  "submission-1",
			OwnerType:     "answer_segment_crop",
			OwnerID:       "registration-1",
			ContentType:   "image/png",
			SizeBytes:     int64(len(data)),
			HashSHA256:    hash,
			StorageBucket: "private-crops",
			StorageKey:    "tenant-1/asset-1.png",
			Visibility:    "private",
		},
	}
	fixture.refresh()
	return fixture
}

func (f *activeCropResolverFixture) refresh() {
	f.evidenceStore = &activeCropEvidenceFake{
		tenantID:   "tenant-1",
		segmentID:  "segment-1",
		questionID: "question-1",
		values:     []segment.SegmentEvidence{f.evidence, f.evidence},
	}
	f.assetStore = &activeCropAssetFake{
		tenantID: "tenant-1",
		assetID:  f.asset.ID,
		values:   []files.FileAsset{f.asset, f.asset},
	}
	f.objects = &activeCropObjectFake{
		bucket: f.asset.StorageBucket,
		key:    f.asset.StorageKey,
		data:   append([]byte{}, f.data...),
	}
	f.resolver = NewActiveCropResolver(f.evidenceStore, f.assetStore, f.objects)
}

type activeCropEvidenceFake struct {
	mu         sync.Mutex
	tenantID   string
	segmentID  string
	questionID string
	values     []segment.SegmentEvidence
	err        error
	calls      int
}

func (f *activeCropEvidenceFake) GetEvidenceForQuestion(_ context.Context, tenantID string, segmentID string, questionID string) (segment.SegmentEvidence, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return segment.SegmentEvidence{}, f.err
	}
	if tenantID != f.tenantID || segmentID != f.segmentID || questionID != f.questionID || len(f.values) == 0 {
		return segment.SegmentEvidence{}, segment.ErrNotFound
	}
	index := min(f.calls-1, len(f.values)-1)
	return f.values[index], nil
}

type activeCropAssetFake struct {
	mu       sync.Mutex
	tenantID string
	assetID  string
	values   []files.FileAsset
	err      error
	calls    int
}

func (f *activeCropAssetFake) Create(context.Context, files.CreateAssetInput) (files.FileAsset, error) {
	return files.FileAsset{}, files.ErrInvalidFile
}

func (f *activeCropAssetFake) FindDuplicate(context.Context, string, string, string, string) (files.FileAsset, bool, error) {
	return files.FileAsset{}, false, files.ErrInvalidFile
}

func (f *activeCropAssetFake) Get(_ context.Context, tenantID string, assetID string) (files.FileAsset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return files.FileAsset{}, f.err
	}
	if tenantID != f.tenantID || assetID != f.assetID || len(f.values) == 0 {
		return files.FileAsset{}, files.ErrNotFound
	}
	index := min(f.calls-1, len(f.values)-1)
	return f.values[index], nil
}

func (f *activeCropAssetFake) Delete(context.Context, string, string) (files.FileAsset, error) {
	return files.FileAsset{}, files.ErrInvalidFile
}

type activeCropObjectFake struct {
	mu       sync.Mutex
	bucket   string
	key      string
	data     []byte
	err      error
	getCalls int
}

func (f *activeCropObjectFake) Put(context.Context, string, string, io.Reader, int64, string) error {
	return files.ErrStorageFailure
}

func (f *activeCropObjectFake) Get(_ context.Context, bucket string, key string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	if f.err != nil {
		return nil, f.err
	}
	if bucket != f.bucket || key != f.key {
		return nil, files.ErrStorageFailure
	}
	return io.NopCloser(bytes.NewReader(f.data)), nil
}

func (f *activeCropObjectFake) Remove(context.Context, string, string) error {
	return files.ErrStorageFailure
}
