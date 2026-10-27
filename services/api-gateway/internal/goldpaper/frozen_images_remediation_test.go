package goldpaper_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/calibration"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/goldpaper"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/seedquality"
)

type frozenImageFixture struct {
	gold    *goldpaper.MemoryStore
	files   *files.MemoryStore
	objects *files.MemoryObjectStorage
	images  *goldpaper.ImageService
	source  goldpaper.Source
	item    goldpaper.GoldPaper
	asset   files.FileAsset
}

func newFrozenImageFixture(t *testing.T) *frozenImageFixture {
	t.Helper()
	f := &frozenImageFixture{gold: goldpaper.NewMemoryStore(), files: files.NewMemoryStore(), objects: files.NewMemoryObjectStorage()}
	f.images = goldpaper.NewImageService(f.gold, f.files, f.objects)
	f.source = goldpaper.Source{ExamID: "exam", QuestionID: "question", SubmissionID: "submission", SnapshotID: "snapshot", ArchetypeCode: "short_constructed", RiskTier: "R2", MaxScore: 5, RubricSnapshot: map[string]any{}, AvailableGradeIDs: []string{"grade"}}
	f.asset = f.addImage(t, "run-original", "original-answer")
	f.setCurrentImage(f.asset)
	var err error
	f.item, err = f.gold.Nominate(t.Context(), "tenant", "exam", "question", "nominator", goldpaper.NominateInput{SubmissionID: "submission", ReferenceScore: 2, Explanation: "original reference", SourceGradeIDs: []string{"grade"}})
	if err != nil {
		t.Fatal(err)
	}
	f.item, err = f.gold.Approve(t.Context(), "tenant", f.item.ID, "approver", 1)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *frozenImageFixture) addImage(t *testing.T, owner, data string) files.FileAsset {
	t.Helper()
	digest := sha256.Sum256([]byte(data))
	asset, err := f.files.Create(t.Context(), files.CreateAssetInput{TenantID: "tenant", ExamID: "exam", OwnerType: "answer_segment_crop", OwnerID: owner, OriginalName: "answer.png", ContentType: "image/png", SizeBytes: int64(len(data)), HashSHA256: hex.EncodeToString(digest[:]), StorageBucket: "synthetic", StorageKey: owner})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.objects.Put(t.Context(), asset.StorageBucket, asset.StorageKey, strings.NewReader(data), asset.SizeBytes, asset.ContentType); err != nil {
		t.Fatal(err)
	}
	return asset
}

func (f *frozenImageFixture) setCurrentImage(asset files.FileAsset) {
	f.source.ImageBinding = goldpaper.ImageBinding{SegmentID: "segment", AssetID: asset.ID, SHA256: asset.HashSHA256, OwnerType: asset.OwnerType, OwnerID: asset.OwnerID}
	f.gold.SetSource("tenant", f.source)
}

func imageRequest(handler http.Handler, method, url, userID string, permissions ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, url, nil)
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: userID, TenantID: "tenant", Permissions: permissions}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func (f *frozenImageFixture) managerRoutes() http.Handler {
	mux := http.NewServeMux()
	guard := func(next http.HandlerFunc) http.Handler { return auth.RequirePermission("review:manage")(next) }
	goldpaper.RegisterRoutes(mux, goldpaper.NewHandler(f.gold, nil).WithImageReader(f.images), guard, guard)
	return mux
}

// 同一题块更换当前来源后，既有 Gold 与已创建会话仍读取原资产；新版本才读取新图。
func TestFrozenGoldImageSurvivesSourceReplacementAndNewVersion(t *testing.T) {
	f := newFrozenImageFixture(t)
	calib := calibration.NewService(calibration.NewMemoryStore(), f.gold)
	_, err := calib.PutPolicy(t.Context(), "tenant", "exam", "question", calibration.PutPolicyInput{ArchetypeCode: "short_constructed", MaxScore: 5, MinimumSamples: 1, MaximumMAE: 0, MinimumExactAgreement: 1, MinimumWithinOneAgreement: 1, SevereErrorThreshold: 1, QualificationValidityDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	session, err := calib.CreateSession(t.Context(), "tenant", "exam", "question", "grader")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	grade := func(next http.HandlerFunc) http.Handler { return auth.RequirePermission("review:work")(next) }
	calibration.RegisterRoutes(mux, calibration.NewHandler(calib, nil).WithImageReader(f.images), grade, grade, grade)
	oldURL := session.Samples[0].AnswerImageURL
	f.setCurrentImage(f.addImage(t, "run-corrected", "corrected-answer"))
	for _, tc := range []struct {
		handler http.Handler
		url     string
	}{{f.managerRoutes(), f.item.AnswerImageURL}, {mux, oldURL}} {
		response := imageRequest(tc.handler, "GET", tc.url, "grader", "review:work", "review:manage")
		if response.Code != 200 || response.Body.String() != "original-answer" {
			t.Fatalf("frozen read changed: %d %q", response.Code, response.Body.String())
		}
	}
	for _, tc := range []struct {
		url, actor string
		want       int
	}{{oldURL, "grader", 200}, {oldURL, "other-grader", 403}, {strings.Replace(oldURL, f.item.ID, "other-gold", 1), "grader", 400}} {
		response := imageRequest(mux, "GET", tc.url, tc.actor, "review:work")
		if response.Code != tc.want {
			t.Fatalf("session scope %s actor %s: %d %s", tc.url, tc.actor, response.Code, response.Body.String())
		}
	}
	loaded, err := calib.GetSession(t.Context(), "tenant", session.ID)
	if err != nil || loaded.GoldSetHash != session.GoldSetHash || loaded.Samples[0].AnswerImageURL != oldURL {
		t.Fatalf("session identity changed: %+v %v", loaded, err)
	}
	_, err = f.gold.CreateVersion(t.Context(), "tenant", f.item.ID, "nominator", goldpaper.CreateVersionInput{ReferenceScore: 3, Explanation: "new image reference", SourceGradeIDs: []string{"grade"}})
	if err != nil {
		t.Fatal(err)
	}
	f.item, err = f.gold.Approve(t.Context(), "tenant", f.item.ID, "approver", 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		number int
		want   string
	}{{1, "original-answer"}, {2, "corrected-answer"}} {
		response := imageRequest(f.managerRoutes(), "GET", goldpaper.VersionImageURL(f.item.ID, tc.number), "manager", "review:manage")
		if response.Code != 200 || response.Body.String() != tc.want {
			t.Fatalf("version %d: %d %q", tc.number, response.Code, response.Body.String())
		}
	}
	if response := imageRequest(mux, "GET", oldURL, "grader", "review:work"); response.Code != 200 || response.Body.String() != "original-answer" {
		t.Fatalf("old session followed new active Gold: %d %s", response.Code, response.Body.String())
	}
}

func TestFrozenGoldImageRejectsObjectOverwriteAndDeletedAsset(t *testing.T) {
	f := newFrozenImageFixture(t)
	if err := f.objects.Put(t.Context(), f.asset.StorageBucket, f.asset.StorageKey, strings.NewReader("tampered-answer"), f.asset.SizeBytes, "image/png"); err != nil {
		t.Fatal(err)
	}
	response := imageRequest(f.managerRoutes(), "GET", f.item.AnswerImageURL, "manager", "review:manage")
	if response.Code != 502 || strings.Contains(response.Body.String(), "tampered-answer") {
		t.Fatalf("unverified bytes escaped: %d %s", response.Code, response.Body.String())
	}
	if _, err := f.files.Delete(t.Context(), "tenant", f.asset.ID); err != nil {
		t.Fatal(err)
	}
	response = imageRequest(f.managerRoutes(), "GET", f.item.AnswerImageURL, "manager", "review:manage")
	if response.Code != 409 {
		t.Fatalf("deleted frozen asset must fail closed: %d %s", response.Code, response.Body.String())
	}
	response = imageRequest(f.managerRoutes(), "GET", f.item.AnswerImageURL, "grader", "review:work")
	if response.Code != 403 {
		t.Fatalf("Gold route leaked to grader: %d", response.Code)
	}
}

type countedFrozenObjects struct {
	files.ObjectStorage
	reads int
}

func (s *countedFrozenObjects) Get(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	s.reads++
	return s.ObjectStorage.Get(ctx, bucket, key)
}

func TestFrozenGoldHeadAndConditionalReadDoNotOpenObjects(t *testing.T) {
	f := newFrozenImageFixture(t)
	objects := &countedFrozenObjects{ObjectStorage: f.objects}
	f.images = goldpaper.NewImageService(f.gold, f.files, objects)
	routes := f.managerRoutes()
	response := imageRequest(routes, "HEAD", f.item.AnswerImageURL, "manager", "review:manage")
	if response.Code != 200 || response.Body.Len() != 0 || objects.reads != 0 || response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("HEAD opened image: %d %s reads=%d", response.Code, response.Body.String(), objects.reads)
	}
	req := httptest.NewRequest("GET", f.item.AnswerImageURL, nil)
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: "manager", TenantID: "tenant", Permissions: []string{"review:manage"}}))
	req.Header.Set("If-None-Match", response.Header().Get("ETag"))
	response = httptest.NewRecorder()
	routes.ServeHTTP(response, req)
	if response.Code != 304 || objects.reads != 0 {
		t.Fatalf("conditional opened image: %d reads=%d", response.Code, objects.reads)
	}
	response = imageRequest(routes, "GET", f.item.AnswerImageURL, "manager", "review:manage")
	if response.Code != 200 || objects.reads != 1 || response.Body.String() != "original-answer" {
		t.Fatalf("ordinary read failed: %d reads=%d", response.Code, objects.reads)
	}
}

type allowFrozenQualification struct{}

func (allowFrozenQualification) RequireQualification(context.Context, string, string, string, string) error {
	return nil
}

func TestSeedImageDelegationKeepsFrozenVersionAndTaskOwnership(t *testing.T) {
	f := newFrozenImageFixture(t)
	seed := seedquality.NewService(seedquality.NewMemoryStore(), f.gold, allowFrozenQualification{})
	_, err := seed.PutPolicy(t.Context(), "tenant", "exam", "question", "manager", seedquality.PutPolicyInput{Rate: 1, MinInterval: 1, MaxInterval: 1, Status: seedquality.PolicyActive})
	if err != nil {
		t.Fatal(err)
	}
	task, issued, err := seed.MaybeIssue(t.Context(), "tenant", "exam", "question", "1", "grader")
	if err != nil || !issued {
		t.Fatalf("issue=%v %v", issued, err)
	}
	f.setCurrentImage(f.addImage(t, "run-corrected", "corrected-answer"))
	handler := review.NewHandler(review.NewMemoryStore(), auth.NewMemoryStore(), nil, nil).WithSeedHook(seed).WithGoldImageReader(f.images)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/review-tasks/{id}/segment-image", handler.GetWorkspaceSegmentImage)
	response := imageRequest(mux, "GET", task.AnswerImageURL, "grader", "review:work")
	if response.Code != 200 || response.Body.String() != "original-answer" || strings.Contains(response.Header().Get("Content-Disposition"), "gold") {
		t.Fatalf("Seed fixed delegation failed: %d %s %s", response.Code, response.Header(), response.Body.String())
	}
	response = imageRequest(mux, "GET", task.AnswerImageURL, "other-grader", "review:work")
	if response.Code != 403 {
		t.Fatalf("Seed image crossed owner: %d %s", response.Code, response.Body.String())
	}
}

func TestGoldNominationRejectsMissingFrozenImageIdentity(t *testing.T) {
	store := goldpaper.NewMemoryStore()
	store.SetSource("tenant", goldpaper.Source{ExamID: "exam", QuestionID: "question", SubmissionID: "submission", SnapshotID: "snapshot", MaxScore: 5, AvailableGradeIDs: []string{"grade"}})
	_, err := store.Nominate(t.Context(), "tenant", "exam", "question", "manager", goldpaper.NominateInput{SubmissionID: "submission", ReferenceScore: 2, Explanation: "missing image", SourceGradeIDs: []string{"grade"}})
	if err != goldpaper.ErrImageUnavailable {
		t.Fatalf("missing image accepted: %v", err)
	}
}
