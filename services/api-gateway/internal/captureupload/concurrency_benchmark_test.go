package captureupload

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"math/rand/v2"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const captureBenchmarkFiles = 32
const captureBenchmarkChunkBytes = 64 * 1024

// BenchmarkCaptureUploadConcurrency 使用迁移后的隔离 PostgreSQL 和专用 MinIO 桶。
// 每路独占采集批次，同考试组共享收卷行，跨考试组每路使用一场考试。只测原件确认，不包含 OCR。
func BenchmarkCaptureUploadConcurrency(b *testing.B) {
	pg, err := pgx.ParseConfig(os.Getenv("EDUGRADE_CAPTURE_BENCH_DATABASE_URL"))
	if err != nil || !regexp.MustCompile(`^edugrade_ci_shared_[a-f0-9]{32}_test$`).MatchString(pg.Database) {
		b.Fatal("EDUGRADE_CAPTURE_BENCH_DATABASE_URL must refer to a migrated prepare-postgres-tests fixture")
	}
	endpoint := os.Getenv("EDUGRADE_CAPTURE_BENCH_MINIO_ENDPOINT")
	if endpoint == "" {
		b.Fatal("EDUGRADE_CAPTURE_BENCH_MINIO_ENDPOINT must point to a disposable MinIO instance")
	}
	bucket := captureBenchmarkEnv("EDUGRADE_CAPTURE_BENCH_MINIO_BUCKET", "capture-bench")
	objects, err := files.NewMinIOObjectStorage(config.MinIOConfig{
		Endpoint: endpoint, AccessKey: captureBenchmarkEnv("EDUGRADE_CAPTURE_BENCH_MINIO_ACCESS_KEY", "edugrade-bench"),
		SecretKey: captureBenchmarkEnv("EDUGRADE_CAPTURE_BENCH_MINIO_SECRET_KEY", "minio-local-bench"),
	})
	if err != nil {
		b.Fatal(err)
	}
	for _, examMode := range []string{"same_exam", "different_exams"} {
		for _, lanes := range []int{1, 2, 4} {
			b.Run(fmt.Sprintf("%s/%d_lanes", examMode, lanes), func(b *testing.B) {
				runCaptureUploadBenchmark(b, pg, objects, bucket, examMode == "same_exam", lanes)
			})
		}
	}
}

func captureBenchmarkEnv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

type captureBenchmarkTarget struct {
	exam  string
	batch string
}

type captureBenchmarkFile struct {
	name  string
	hash  string
	data  []byte
	chunk []ChunkInput
}

type captureBenchmarkResult struct {
	chunks     []time.Duration
	completion time.Duration
	err        error
}

type captureBenchmarkLockSamples struct {
	total   int
	blocked int
	peak    int
	err     error
}

func runCaptureUploadBenchmark(b *testing.B, configPG *pgx.ConnConfig, objects *files.MinIOObjectStorage, bucket string, sameExam bool, lanes int) {
	b.Helper()
	if b.N != 1 {
		b.Fatal("use -benchtime=1x to keep the same fixed work per benchmark sample")
	}
	ctx, cancel := context.WithTimeout(b.Context(), 2*time.Minute)
	defer cancel()
	appName := "capture-bench-" + uuid.NewString()
	pg := configPG.Copy()
	pg.RuntimeParams["application_name"] = appName
	pool := stdlib.OpenDB(*pg)
	b.Cleanup(func() { _ = pool.Close() })
	pool.SetMaxOpenConns(10)
	pool.SetMaxIdleConns(10)
	observer := stdlib.OpenDB(*configPG)
	b.Cleanup(func() { _ = observer.Close() })
	if err := observer.PingContext(ctx); err != nil {
		b.Fatal(err)
	}

	captures := capture.NewPostgresStore(pool)
	service := NewService(NewPostgresStore(pool), captures, files.NewPostgresStore(pool), objects, config.FileConfig{Bucket: bucket})
	tenant, actor, targets := seedCaptureUploadLanes(b, ctx, pool, captures, sameExam, lanes)
	b.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		entries, err := objects.List(cleanup, bucket, "tenant/"+tenant+"/files/", captureBenchmarkFiles+1)
		if err != nil {
			b.Error(err)
			return
		}
		for _, object := range entries {
			if err := objects.Remove(cleanup, bucket, object.Key); err != nil {
				b.Error(err)
			}
		}
	})
	payloads := captureBenchmarkPayloads(b)
	ready := make(chan struct{})
	stopSamples := make(chan struct{})
	sampled := make(chan captureBenchmarkLockSamples, 1)
	results := make(chan captureBenchmarkResult, len(payloads))
	var workers sync.WaitGroup
	for lane, target := range targets {
		workers.Go(func() {
			<-ready
			for index := lane; index < len(payloads); index += lanes {
				results <- uploadCaptureBenchmarkFile(ctx, service, tenant, actor, target, payloads[index])
			}
		})
	}

	// 采样使用另一个池，不计入业务池等待。采样值表示观察到的阻塞连接数。
	go func() { sampled <- sampleCaptureBenchmarkLocks(ctx, observer, appName, stopSamples) }()
	before := pool.Stats()
	b.ResetTimer()
	started := time.Now()
	close(ready)
	workers.Wait()
	elapsed := time.Since(started)
	b.StopTimer()
	close(results)
	close(stopSamples)
	after := pool.Stats()
	locks := <-sampled
	if locks.err != nil {
		b.Fatal(locks.err)
	}

	var chunkTimes, completeTimes []time.Duration
	for result := range results {
		if result.err != nil {
			b.Error(result.err)
			continue
		}
		chunkTimes = append(chunkTimes, result.chunks...)
		completeTimes = append(completeTimes, result.completion)
	}
	if len(completeTimes) != len(payloads) {
		b.Fatalf("confirmed %d of %d uploads", len(completeTimes), len(payloads))
	}
	verifyCaptureBenchmarkFiles(b, ctx, pool, objects, bucket, tenant)
	b.ReportMetric(float64(len(completeTimes))/elapsed.Seconds(), "files/s")
	b.ReportMetric(captureBenchmarkP95(chunkTimes), "chunk_p95_ms")
	b.ReportMetric(captureBenchmarkP95(completeTimes), "complete_p95_ms")
	b.ReportMetric(float64(after.WaitCount-before.WaitCount), "pool_waits")
	b.ReportMetric(float64((after.WaitDuration-before.WaitDuration).Microseconds())/1000, "pool_wait_ms")
	b.ReportMetric(float64(locks.total), "pg_samples")
	b.ReportMetric(float64(locks.blocked), "pg_chunk_lock_samples")
	b.ReportMetric(float64(locks.peak), "pg_chunk_lock_peak")
}

func seedCaptureUploadLanes(b testing.TB, ctx context.Context, pool *sql.DB, captures capture.Store, sameExam bool, lanes int) (string, string, []captureBenchmarkTarget) {
	b.Helper()
	tenant, school, actor := uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec := func(query string, args ...any) {
		b.Helper()
		if _, err := pool.ExecContext(ctx, query, args...); err != nil {
			b.Fatal(err)
		}
	}
	exec(`INSERT INTO tenant(id,tenant_id,name,code,status) VALUES($1::uuid,$1::uuid,'Synthetic upload benchmark',$2,'active')`, tenant, "upload-bench-"+tenant)
	exec(`INSERT INTO school(id,tenant_id,name,code,status) VALUES($1,$2,'Synthetic upload school','upload-bench','active')`, school, tenant)
	exec(`INSERT INTO app_user(id,tenant_id,school_id,username,display_name,password_hash,status)
VALUES($1,$2,$3,'synthetic-uploader','Synthetic uploader','not-used','active')`, actor, tenant, school)
	targets := make([]captureBenchmarkTarget, 0, lanes)
	var exam string
	for lane := range lanes {
		if lane == 0 || !sameExam {
			exam = uuid.NewString()
			exec(`INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by)
VALUES($1,$2,$3,$4,'math','quiz',10,'collecting','ai_assisted','manual_after_confirmation',$5)`,
				exam, tenant, school, fmt.Sprintf("Synthetic upload exam %d", lane), actor)
		}
		batch, err := captures.CreateBatch(ctx, tenant, exam, actor, capture.CreateBatchInput{
			Name: fmt.Sprintf("Synthetic scanner %d", lane), SourceType: "desktop_sync",
		})
		if err != nil {
			b.Fatal(err)
		}
		targets = append(targets, captureBenchmarkTarget{exam: exam, batch: batch.ID})
	}
	return tenant, actor, targets
}

func captureBenchmarkPayloads(b *testing.B) []captureBenchmarkFile {
	b.Helper()
	data := make([]captureBenchmarkFile, captureBenchmarkFiles)
	pixels := image.NewGray(image.Rect(0, 0, 512, 512))
	noise := rand.New(rand.NewPCG(1763, 1229))
	for index := range pixels.Pix {
		pixels.Pix[index] = byte(noise.Uint32())
	}
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	for index := range data {
		// 文件大小近似，内容摘要不同，避免复用既有对象带来虚假吞吐。
		pixels.Pix[0] = byte(index)
		var encoded bytes.Buffer
		if err := encoder.Encode(&encoded, pixels); err != nil {
			b.Fatal(err)
		}
		item := captureBenchmarkFile{name: fmt.Sprintf("synthetic-page-%02d.png", index), data: encoded.Bytes()}
		item.hash = chunkHash(item.data)
		for offset := 0; offset < len(item.data); offset += captureBenchmarkChunkBytes {
			payload := item.data[offset:min(offset+captureBenchmarkChunkBytes, len(item.data))]
			item.chunk = append(item.chunk, ChunkInput{Offset: int64(offset), Data: payload, SHA256: chunkHash(payload)})
		}
		data[index] = item
	}
	return data
}

func uploadCaptureBenchmarkFile(ctx context.Context, service *Service, tenant, actor string, target captureBenchmarkTarget, file captureBenchmarkFile) captureBenchmarkResult {
	upload, err := service.Init(ctx, tenant, actor, InitInput{
		SHA256: file.hash, Size: int64(len(file.data)), MIME: "image/png", ExamID: target.exam,
		BatchID: target.batch, IdempotencyKey: file.name, Filename: file.name,
	})
	if err != nil || upload.AlreadyExists {
		return captureBenchmarkResult{err: fmt.Errorf("init %s: already_exists=%t err=%v", file.name, upload.AlreadyExists, err)}
	}
	var result captureBenchmarkResult
	for _, chunk := range file.chunk {
		started := time.Now()
		receipt, err := service.AppendChunk(ctx, tenant, upload.RemoteUploadID, chunk)
		result.chunks = append(result.chunks, time.Since(started))
		if err != nil || receipt.ConfirmedOffset != chunk.Offset+int64(len(chunk.Data)) {
			return captureBenchmarkResult{err: fmt.Errorf("append %s: offset=%d err=%v", file.name, receipt.ConfirmedOffset, err)}
		}
	}
	started := time.Now()
	receipt, err := service.Complete(ctx, tenant, actor, upload.RemoteUploadID, CompleteInput{SHA256: file.hash})
	result.completion = time.Since(started)
	if err != nil || receipt.Status != "completed" || receipt.CaptureFileID == "" || receipt.FileAssetID == "" || receipt.SHA256 != file.hash {
		return captureBenchmarkResult{err: fmt.Errorf("complete %s: status=%q err=%v", file.name, receipt.Status, err)}
	}
	return result
}

func sampleCaptureBenchmarkLocks(ctx context.Context, observer *sql.DB, appName string, done <-chan struct{}) captureBenchmarkLockSamples {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	var stats captureBenchmarkLockSamples
	for {
		select {
		case <-done:
			return stats
		case <-ticker.C:
			var blocked int
			// 只采样确认分块偏移的更新。每路会话和批次独立，收卷触发器仍会访问同考试的集合行。
			err := observer.QueryRowContext(ctx, `
SELECT count(*) FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock'
 AND ltrim(query, E'\n\r\t ') LIKE 'UPDATE capture_upload_session%SET confirmed_offset=%'
 AND cardinality(pg_blocking_pids(pid)) > 0`, appName).Scan(&blocked)
			if err != nil {
				stats.err = err
				return stats
			}
			stats.total++
			stats.blocked += blocked
			stats.peak = max(stats.peak, blocked)
		}
	}
}

func verifyCaptureBenchmarkFiles(b *testing.B, ctx context.Context, pool *sql.DB, objects files.ObjectStorage, bucket, tenant string) {
	b.Helper()
	rows, err := pool.QueryContext(ctx, `
SELECT s.capture_file_id::text, s.file_asset_id::text, a.storage_key, s.expected_sha256
FROM capture_upload_session s
JOIN capture_file f ON f.tenant_id=s.tenant_id AND f.id=s.capture_file_id AND f.file_asset_id=s.file_asset_id
JOIN file_asset a ON a.tenant_id=s.tenant_id AND a.id=s.file_asset_id AND a.hash_sha256=s.expected_sha256
WHERE s.tenant_id=$1 AND s.status='completed' AND f.sha256=s.expected_sha256 AND s.confirmed_offset=s.total_size
 AND NOT EXISTS(SELECT 1 FROM capture_upload_chunk c WHERE c.tenant_id=s.tenant_id AND c.capture_upload_session_id=s.id)`, tenant)
	if err != nil {
		b.Fatal(err)
	}
	defer rows.Close()
	registered := make(map[string]bool)
	assets := make(map[string]bool)
	for rows.Next() {
		var id, asset, key, hash string
		if err := rows.Scan(&id, &asset, &key, &hash); err != nil {
			b.Fatal(err)
		}
		if registered[id] || assets[asset] {
			b.Fatalf("duplicate or aliased capture registration %s", id)
		}
		registered[id], assets[asset] = true, true
		reader, err := objects.Get(ctx, bucket, key)
		if err != nil {
			b.Fatal(err)
		}
		body, readError := io.ReadAll(reader)
		if err := errors.Join(readError, reader.Close()); err != nil {
			b.Fatal(err)
		}
		if chunkHash(body) != hash {
			b.Fatalf("object checksum differs from receipt for registration %s", id)
		}
	}
	if err := rows.Err(); err != nil {
		b.Fatal(err)
	}
	if len(registered) != captureBenchmarkFiles {
		b.Fatalf("verified %d of %d persisted registrations", len(registered), captureBenchmarkFiles)
	}
}

func captureBenchmarkP95(values []time.Duration) float64 {
	slices.Sort(values)
	return float64(values[(len(values)*95+99)/100-1].Microseconds()) / 1000
}

func TestPostgresCaptureUploadCollectionLockScope(t *testing.T) {
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("YUEJUAN_BINDING_TEST_DATABASE_URL is not configured")
	}
	pg, err := pgx.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(pg.Database, "_test") {
		t.Fatal("expected PostgreSQL fixture ending in _test")
	}
	appName := "capture-lock-" + uuid.NewString()
	pg.RuntimeParams["application_name"] = appName
	pool := stdlib.OpenDB(*pg)
	t.Cleanup(func() { _ = pool.Close() })
	pool.SetMaxOpenConns(4)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	captures := capture.NewPostgresStore(pool)
	service := NewService(NewPostgresStore(pool), captures, nil, nil, config.FileConfig{})
	tenant, actor, lanes := seedCaptureUploadLanes(t, ctx, pool, captures, false, 2)
	chunk := ChunkInput{Data: []byte("synthetic scan chunk")}
	chunk.SHA256 = chunkHash(chunk.Data)
	var uploadIDs [2]string
	for index, lane := range lanes {
		upload, err := service.Init(ctx, tenant, actor, InitInput{
			SHA256: chunk.SHA256, Size: int64(len(chunk.Data)), MIME: "image/png",
			ExamID: lane.exam, BatchID: lane.batch, IdempotencyKey: "collection-lock",
		})
		if err != nil {
			t.Fatal(err)
		}
		uploadIDs[index] = upload.RemoteUploadID
	}

	// 外部事务只持有第一场考试的收卷行；第二场的偏移确认必须在释放前提交。
	blocker, err := pool.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	var blockerPID int
	if err := blocker.QueryRowContext(ctx, `SELECT pg_backend_pid() FROM capture_collection WHERE tenant_id=$1 AND exam_id=$2 FOR UPDATE`,
		tenant, lanes[0].exam).Scan(&blockerPID); err != nil {
		t.Fatal(err)
	}
	first := make(chan error, 1)
	appendChunk := func(uploadID string) error {
		receipt, err := service.AppendChunk(ctx, tenant, uploadID, chunk)
		if err != nil {
			return err
		}
		if receipt.ConfirmedOffset != int64(len(chunk.Data)) {
			return fmt.Errorf("confirmed offset = %d, expected %d", receipt.ConfirmedOffset, len(chunk.Data))
		}
		return nil
	}
	go func() { first <- appendChunk(uploadIDs[0]) }()
	waitCtx, stopWaiting := context.WithTimeout(ctx, 10*time.Second)
	defer stopWaiting()
	if err := waitForCaptureCollectionBlocker(waitCtx, pool, appName, blockerPID, first); err != nil {
		t.Fatal(err)
	}
	if err := appendChunk(uploadIDs[1]); err != nil {
		t.Fatalf("append to another exam while collector is locked: %v", err)
	}
	select {
	case err := <-first:
		t.Fatalf("same exam completed while collector was locked: %v", err)
	default:
	}
	if err := blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-first; err != nil {
		t.Fatalf("same-exam upload did not resume after release: %v", err)
	}
}

func waitForCaptureCollectionBlocker(ctx context.Context, pool *sql.DB, appName string, blockerPID int, first <-chan error) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("did not observe the same-exam append blocked on the collection row: %w", ctx.Err())
		case err := <-first:
			return fmt.Errorf("same-exam append exited before waiting for the collection row: %v", err)
		case <-ticker.C:
			var blocked bool
			err := pool.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock'
   AND ltrim(query, E'\n\r\t ') LIKE 'UPDATE capture_upload_session%SET confirmed_offset=%'
   AND $2=ANY(pg_blocking_pids(pid))
)`, appName, blockerPID).Scan(&blocked)
			if err != nil || blocked {
				return err
			}
		}
	}
}
