package exam

import (
	"context"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

type managementBulkCheckSpy struct {
	*MemoryStore
	bulkCalls   int
	singleCalls int
	checks      map[string]ManagementArchiveCheck
	err         error
}

func (s *managementBulkCheckSpy) CheckManagementArchives(context.Context, auth.AccessScope, []ManagementExam) (map[string]ManagementArchiveCheck, error) {
	s.bulkCalls++
	return s.checks, s.err
}

func (s *managementBulkCheckSpy) CheckManagementArchive(context.Context, auth.AccessScope, string, string) (ManagementArchiveCheck, error) {
	s.singleCalls++
	return ManagementArchiveCheck{Allowed: true}, nil
}

func TestManagementListDecoratesWithOneBulkCheckAndKeepsScopeRestrictions(t *testing.T) {
	spy := &managementBulkCheckSpy{MemoryStore: NewMemoryStore(), checks: map[string]ManagementArchiveCheck{
		"session:a": {Allowed: true}, "session:b": {Allowed: true},
	}}
	items := []ManagementExam{{ID: "a", Kind: "session", CanManage: true}, {ID: "b", Kind: "session", CanManage: false}}
	h := NewHandler(spy, nil)
	h.decorateManagementArchives(context.Background(), auth.AccessScope{}, items)
	if spy.bulkCalls != 1 || spy.singleCalls != 0 || !items[0].CanArchive || items[1].CanArchive {
		t.Fatalf("bulk check repeated or expanded scope: calls=%d/%d items=%#v", spy.bulkCalls, spy.singleCalls, items)
	}
	// 已在仓库快照中检查过的页面不能再次启动每场检查，避免资格和显示状态来自不同快照。
	h.decorateManagementArchives(context.Background(), auth.AccessScope{}, items)
	if spy.bulkCalls != 1 || spy.singleCalls != 0 {
		t.Fatalf("resolved page queried again: %d/%d", spy.bulkCalls, spy.singleCalls)
	}
}

func TestManagementBulkFailureOrMissingResultDisablesArchive(t *testing.T) {
	for _, fail := range []bool{false, true} {
		spy := &managementBulkCheckSpy{MemoryStore: NewMemoryStore(), checks: map[string]ManagementArchiveCheck{"session:a": {Allowed: true}}}
		if fail {
			spy.err = errors.New("runtime facts unavailable")
		}
		items := []ManagementExam{{ID: "a", Kind: "session", CanManage: true}, {ID: "b", Kind: "session", CanManage: true}}
		NewHandler(spy, nil).decorateManagementArchives(context.Background(), auth.AccessScope{}, items)
		if fail && items[0].CanArchive || items[1].CanArchive || len(items[1].ArchiveBlockers) != 1 || items[1].ArchiveBlockers[0].Code != "archive_check_unavailable" {
			t.Fatalf("unknown result became permission (fail=%v): %#v", fail, items)
		}
	}
}

func TestMemoryManagementListReturnsChecksFromItsReadSnapshot(t *testing.T) {
	s, scope, _ := managementFixture(t)
	items, err := s.ListManagementExams(context.Background(), scope, ManagementListFilter{Limit: 1})
	if err != nil || len(items) != 1 || !items[0].archiveCheckResolved || !items[0].CanArchive {
		t.Fatalf("list snapshot did not include archive check: %#v %v", items, err)
	}
}
