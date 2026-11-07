package main

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestBatch9CropRepairSkipAndUpdate(t *testing.T) {
	// 内容哈希和归属都匹配才跳过；修复路径必须先上传证据再更新原分段。
	for _, matching := range []bool{true, false} {
		t.Run(strconv.FormatBool(matching), func(t *testing.T) {
			cfg, data := batch9DatasetFixture(t)
			data.Candidates, data.Questions = data.Candidates[:1], data.Questions[:1]
			source, err := decodeImage(filepath.Join(cfg.datasetDir, "page1.png"))
			if err != nil {
				t.Fatal(err)
			}
			crop, _, err := cropAnswerRegion(source, data.Regions["C001"]["Q1"], cropPadding)
			if err != nil {
				t.Fatal(err)
			}
			hash, ownerType, ownerID := "old", "wrong", "wrong"
			if matching {
				hash, ownerType, ownerID = sha256Hex(crop), "answer_segment_crop", "registration"
			}
			script := &batch9SQLScript{affected: 1, queryRows: [][]driver.Value{{"segment", "C001", "Q1", "registration", hash, ownerType, ownerID}}}
			calls := 0
			client := batch9Client(func(request *http.Request) (*http.Response, error) {
				calls++
				values, filename, content := readBatch9Multipart(t, request)
				if !reflect.DeepEqual(values, map[string]string{"owner_type": "answer_segment_crop", "owner_id": "registration", "exam_id": "exam"}) || filename != "c001-q1-crop.png" || !bytes.Equal(content, crop) {
					t.Fatalf("repair upload = %#v %s", values, filename)
				}
				return batch9Response(request, 201, `{"file":{"id":"crop","hash_sha256":"new-hash"}}`), nil
			})
			result, err := repairExistingCrops(context.Background(), cfg, data, client, openBatch9DB(t, script), "tenant", "exam")
			if err != nil || result.Found != 1 || result.Updated+result.Skipped != 1 {
				t.Fatalf("repair = %#v %v", result, err)
			}
			assertBatch9CropRepairRoute(t, matching, result, calls, script)
			logBatch9Proof(t, "repair", map[string]any{"result": result, "calls": calls, "sql": script.events})
		})
	}
}

func assertBatch9CropRepairRoute(t *testing.T, matching bool, result cropRepairResult, calls int, script *batch9SQLScript) {
	t.Helper()
	if matching && (result.Skipped != 1 || calls != 0 || script.execCount != 0) || !matching && (result.Updated != 1 || calls != 1 || script.execCount != 1) {
		t.Fatalf("repair route = %#v calls=%d SQL=%#v", result, calls, script.events)
	}
}

func assertBatch9Error(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func batch9ErrorText(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

func logBatch9Proof(t *testing.T, name string, value any) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"case": t.Name() + "/" + name, "state": value})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("FUJIAN_IMPORT_BATCH9_PROOF %s", raw)
}
