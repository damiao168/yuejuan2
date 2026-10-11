package questionbank

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"strings"
	"testing"
)

func TestMemberIdentityManagerReceivesAccountsForSameNames(t *testing.T) {
	store, script := openBatch22Store(t, []batch22Step{
		batch22BankStep(batch22BankID, "active"),
		batch22BankStep(batch22BankID, "active"),
		{kind: "query", fragment: "CASE WHEN $4 THEN username ELSE '' END", columns: 4,
			args: map[int]any{0: "tenant", 1: batch22BankID, 2: "school", 3: true},
			rows: [][]driver.Value{
				{"math", "王老师", "wang.math", []byte(`["read","review"]`)},
				{"physics", "王老师", "wang.physics", []byte(`["read"]`)},
			}},
	})
	members, err := store.ListMembers(context.Background(), batch22Scope(), batch22BankID)
	if err != nil || len(members) != 2 || members[0].Username != "wang.math" || members[1].Username != "wang.physics" {
		t.Fatalf("same-name identities: %#v, %v", members, err)
	}
	assertBatch22Script(t, script, "query:CASE WHEN $4 THEN username ELSE '' END")
}

func TestMemberIdentityContentReaderDoesNotReceiveLoginAccounts(t *testing.T) {
	// 脚本核对真实查询使用 manage=false；普通内容读者仍只取得已有审核联系人。
	store, script := openBatch22Store(t, []batch22Step{
		batch22BankStep(batch22BankID, "active"),
		{kind: "query", fragment: "question_bank_actor_has_action", columns: 11},
		{kind: "query", fragment: "CASE WHEN $4 THEN username ELSE '' END", columns: 4,
			args: map[int]any{3: false}, rows: [][]driver.Value{{"reviewer", "王老师", "", []byte(`["read","review"]`)}},
		},
	})
	members, err := store.ListMembers(context.Background(), batch22Scope(), batch22BankID)
	if err != nil || len(members) != 1 || members[0].Username != "" {
		t.Fatalf("reader identity projection: %#v, %v", members, err)
	}
	raw, err := json.Marshal(members)
	if err != nil || strings.Contains(string(raw), "username") {
		t.Fatalf("reader response exposed login account: %s, %v", raw, err)
	}
	assertBatch22Script(t, script, "query:CASE WHEN $4 THEN username ELSE '' END")
}
