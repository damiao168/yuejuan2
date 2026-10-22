package reviewbookmark

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"github.com/google/uuid"
)

type PostgresStore struct {
	db       *sql.DB
	provider SourceProvider
}

func NewPostgresStore(db *sql.DB, provider SourceProvider) *PostgresStore {
	return &PostgresStore{db: db, provider: provider}
}

// WithProvider 仅供应用启动装配；请求开始后不应更换共享 provider。
func (s *PostgresStore) WithProvider(provider SourceProvider) *PostgresStore {
	s.provider = provider
	return s
}

func (s *PostgresStore) resolveTx(ctx context.Context, tx *sql.Tx, user auth.User, task string, lock bool) (Source, error) {
	if err := requireWork(user); err != nil {
		return Source{}, err
	}
	if _, err := uuid.Parse(task); err != nil {
		return Source{}, ErrNotFound
	}
	provider, ok := s.provider.(TransactionalSourceProvider)
	if !ok {
		return Source{}, ErrNotFound
	}
	return provider.ResolveTx(ctx, tx, user, task, lock)
}

const recordColumns = `id::text,tenant_id::text,owner_id::text,task_id::text,COALESCE(review_task_id::text,''),COALESCE(seed_task_id::text,''),reason,revision,marked_at,created_at,updated_at,deleted_at,observed_crop_sha256,observed_snapshot_id`

type rowScanner interface{ Scan(...any) error }

func scanRecord(row rowScanner) (Record, error) {
	var r Record
	err := row.Scan(&r.ID, &r.TenantID, &r.OwnerID, &r.TaskID, &r.ReviewTaskID, &r.SeedTaskID, &r.Reason, &r.Revision, &r.MarkedAt, &r.CreatedAt, &r.UpdatedAt, &r.DeletedAt, &r.ObservedCropSHA256, &r.ObservedSnapshotID)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

func (s *PostgresStore) State(ctx context.Context, user auth.User, task string) (Bookmark, error) {
	if err := requireWork(user); err != nil {
		return Bookmark{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return Bookmark{}, err
	}
	defer tx.Rollback()
	source, err := s.resolveTx(ctx, tx, user, task, false)
	if err != nil {
		return Bookmark{}, err
	}
	record, err := scanRecord(tx.QueryRowContext(ctx, `SELECT `+recordColumns+` FROM review_bookmark WHERE tenant_id=$1::uuid AND owner_id=$2::uuid AND task_id=$3::uuid`, user.TenantID, user.ID, task))
	if errors.Is(err, ErrNotFound) {
		return Bookmark{TaskID: task, CanEditReason: !source.Archived}, tx.Commit()
	}
	if err != nil {
		return Bookmark{}, err
	}
	return project(record, source), tx.Commit()
}

func (s *PostgresStore) GetByID(ctx context.Context, user auth.User, id string) (Record, error) {
	if err := requireWork(user); err != nil {
		return Record{}, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return Record{}, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return Record{}, err
	}
	defer tx.Rollback()
	record, err := scanRecord(tx.QueryRowContext(ctx, `SELECT `+recordColumns+` FROM review_bookmark WHERE tenant_id=$1::uuid AND owner_id=$2::uuid AND id=$3::uuid AND deleted_at IS NULL`, user.TenantID, user.ID, id))
	if err != nil {
		return Record{}, err
	}
	if _, err = s.resolveTx(ctx, tx, user, record.TaskID, false); err != nil {
		return Record{}, err
	}
	return record, tx.Commit()
}

func (s *PostgresStore) Put(ctx context.Context, user auth.User, task string, input PutInput) (Acknowledgement, error) {
	original := input
	input, err := normalizePut(input)
	if err != nil {
		return Acknowledgement{}, err
	}
	if err = requireWork(user); err != nil {
		return Acknowledgement{}, err
	}
	ctx = commandreceipt.WithInput(ctx, original)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Acknowledgement{}, err
	}
	defer tx.Rollback()
	identity := commandreceipt.Identity{TenantID: user.TenantID, ActorID: user.ID, Operation: "personal_review_bookmark.put", TargetID: task}
	var ack Acknowledgement
	loaded, err := commandreceipt.Load(ctx, tx, identity, input, &ack)
	if err != nil {
		return ack, err
	}
	// 回执只保存确认信息。命中后仍核验当前源权限，不能借重放读取已经失权的材料。
	source, err := s.resolveTx(ctx, tx, user, task, true)
	if err != nil {
		return Acknowledgement{}, err
	}
	if loaded {
		return ack, tx.Commit()
	}
	if source.Archived {
		return ack, ErrArchived
	}
	// 首次创建没有目标行可锁，统一锁个人任务定位，再检查墓碑版本，避免并发插入绕过 CAS。
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, targetKey(user, task)+":review-bookmark"); err != nil {
		return ack, err
	}
	record, err := scanRecord(tx.QueryRowContext(ctx, `SELECT `+recordColumns+` FROM review_bookmark WHERE tenant_id=$1::uuid AND owner_id=$2::uuid AND task_id=$3::uuid FOR UPDATE`, user.TenantID, user.ID, task))
	if errors.Is(err, ErrNotFound) {
		record = Record{}
		err = nil
	}
	if err != nil {
		return ack, err
	}
	if record.Revision != input.ExpectedRevision {
		return ack, &RevisionConflict{Revision: record.Revision}
	}
	now := time.Now().UTC()
	switch {
	case record.ID == "":
		if (source.ReviewTaskID == "") == (source.SeedTaskID == "") {
			return ack, ErrNotFound
		}
		record.ID = uuid.NewString()
		record.Revision = 1
		_, err = tx.ExecContext(ctx, `INSERT INTO review_bookmark(id,tenant_id,owner_id,review_task_id,seed_task_id,reason,revision,marked_at,created_at,updated_at,observed_crop_sha256,observed_snapshot_id) VALUES($1::uuid,$2::uuid,$3::uuid,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,$6,1,$7,$7,$7,$8,$9)`, record.ID, user.TenantID, user.ID, source.ReviewTaskID, source.SeedTaskID, input.Reason, now, source.ObservedCropSHA256, source.ObservedSnapshotID)
	case record.DeletedAt != nil:
		record.Revision++
		_, err = tx.ExecContext(ctx, `UPDATE review_bookmark SET reason=$4,revision=revision+1,marked_at=$5,updated_at=$5,deleted_at=NULL,observed_crop_sha256=$6,observed_snapshot_id=$7 WHERE tenant_id=$1::uuid AND owner_id=$2::uuid AND id=$3::uuid`, user.TenantID, user.ID, record.ID, input.Reason, now, source.ObservedCropSHA256, source.ObservedSnapshotID)
	case record.Reason != input.Reason:
		record.Revision++
		_, err = tx.ExecContext(ctx, `UPDATE review_bookmark SET reason=$4,revision=revision+1,updated_at=$5 WHERE tenant_id=$1::uuid AND owner_id=$2::uuid AND id=$3::uuid`, user.TenantID, user.ID, record.ID, input.Reason, now)
	}
	if err != nil {
		return ack, mapPostgresError(err)
	}
	ack = Acknowledgement{BookmarkID: record.ID, Revision: record.Revision, Marked: true}
	if err = commandreceipt.Save(ctx, tx, identity, input, ack); err != nil {
		return Acknowledgement{}, err
	}
	return ack, tx.Commit()
}

func (s *PostgresStore) Delete(ctx context.Context, user auth.User, id string, input DeleteInput) (Acknowledgement, error) {
	if err := requireAccount(user); err != nil {
		return Acknowledgement{}, err
	}
	if input.ExpectedRevision < 0 {
		return Acknowledgement{}, ErrInvalid
	}
	if _, err := uuid.Parse(id); err != nil {
		return Acknowledgement{}, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Acknowledgement{}, err
	}
	defer tx.Rollback()
	identity := commandreceipt.Identity{TenantID: user.TenantID, ActorID: user.ID, Operation: "personal_review_bookmark.delete", TargetID: id}
	var ack Acknowledgement
	loaded, err := commandreceipt.Load(ctx, tx, identity, input, &ack)
	if err != nil {
		return ack, err
	}
	// 失去阅卷权仍可清理本人元数据；始终验证本人所有权，不返回源任务和说明。
	record, err := scanRecord(tx.QueryRowContext(ctx, `SELECT `+recordColumns+` FROM review_bookmark WHERE tenant_id=$1::uuid AND owner_id=$2::uuid AND id=$3::uuid FOR UPDATE`, user.TenantID, user.ID, id))
	if err != nil {
		return ack, err
	}
	if loaded {
		return ack, tx.Commit()
	}
	if record.Revision != input.ExpectedRevision {
		return ack, &RevisionConflict{Revision: record.Revision}
	}
	if record.DeletedAt == nil {
		_, err = tx.ExecContext(ctx, `UPDATE review_bookmark SET reason='',revision=revision+1,updated_at=now(),deleted_at=now() WHERE tenant_id=$1::uuid AND owner_id=$2::uuid AND id=$3::uuid`, user.TenantID, user.ID, id)
		record.Revision++
	}
	if err != nil {
		return ack, mapPostgresError(err)
	}
	ack = Acknowledgement{BookmarkID: id, Revision: record.Revision, Marked: false}
	if err = commandreceipt.Save(ctx, tx, identity, input, ack); err != nil {
		return Acknowledgement{}, err
	}
	return ack, tx.Commit()
}

func mapPostgresError(err error) error {
	if err != nil && strings.Contains(err.Error(), "exam_archived") {
		return ErrArchived
	}
	return err
}
