package main

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestBatch9ConfigDefaultsAndModeBoundaries(t *testing.T) {
	for _, name := range []string{"API_BASE_URL", "POSTGRES_DSN", "TENANT", "ADMIN_USER", "ADMIN_PASSWORD", "GRADER_USER"} {
		t.Setenv("EDUGRADE_IMPORT_"+name, "")
	}
	tests := []struct {
		name, password, wantError string
		args                      []string
	}{
		{name: "defaults", password: "test-password"},
		{name: "missing_password", wantError: "EDUGRADE_IMPORT_ADMIN_PASSWORD or -admin-password is required"},
		{name: "negative_ai_limit", password: "test-password", args: []string{"-ai-limit=-1"}, wantError: "ai-limit cannot be negative"},
		{name: "zero_ai_limit", password: "test-password", args: []string{"-ai-limit=0"}},
		{name: "ai_only", password: "test-password", args: []string{"-ai-only"}},
		{name: "repair_only", password: "test-password", args: []string{"-repair-crops"}},
		{name: "reconcile_only", password: "test-password", args: []string{"-reconcile-ocr-runtime"}},
		{name: "ai_and_repair", password: "test-password", args: []string{"-ai-only", "-repair-crops"}, wantError: "cannot be used together"},
		{name: "ai_and_reconcile", password: "test-password", args: []string{"-ai-only", "-reconcile-ocr-runtime"}, wantError: "cannot be used together"},
		{name: "repair_and_reconcile", password: "test-password", args: []string{"-repair-crops", "-reconcile-ocr-runtime"}, wantError: "cannot be used together"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			originalFlags, originalArgs := flag.CommandLine, os.Args
			defer func() { flag.CommandLine, os.Args = originalFlags, originalArgs }()
			flag.CommandLine = flag.NewFlagSet("importer", flag.ContinueOnError)
			os.Args = append([]string{"importer"}, test.args...)
			t.Setenv("EDUGRADE_IMPORT_ADMIN_PASSWORD", test.password)
			cfg, err := parseConfig()
			assertBatch9Error(t, err, test.wantError)
			if test.name == "defaults" {
				assertBatch9ConfigDefaults(t, cfg)
			}
			if test.name == "zero_ai_limit" && cfg.aiLimit != 0 {
				t.Fatalf("zero AI limit = %d", cfg.aiLimit)
			}
			logBatch9Proof(t, test.name, map[string]any{
				"error": batch9ErrorText(err), "apiBase": cfg.apiBaseURL, "dsn": cfg.postgresDSN,
				"tenant": cfg.tenantCode, "admin": cfg.adminUser, "grader": cfg.graderUser,
				"dataset": cfg.datasetDir, "result": cfg.resultPath, "aiLimit": cfg.aiLimit,
				"aiOnly": cfg.aiOnly, "repair": cfg.repairCrops, "reconcile": cfg.reconcileOCRRuntime,
			})
		})
	}
}

func assertBatch9ConfigDefaults(t *testing.T, cfg config) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	datasetDir := filepath.Join(root, "lab", "evals", "synthetic", datasetID)
	assertBatch9ConnectionDefaults(t, cfg)
	assertBatch9IdentityDefaults(t, cfg)
	assertBatch9DatasetModeDefaults(t, cfg, datasetDir)
}

func assertBatch9ConnectionDefaults(t *testing.T, cfg config) {
	t.Helper()
	if cfg.apiBaseURL != "http://127.0.0.1:8080" || cfg.postgresDSN != "postgres://edugrade:change_me_postgres_demo@127.0.0.1:5432/edugrade?sslmode=disable" {
		t.Fatalf("defaults = %#v", cfg)
	}
}

func assertBatch9IdentityDefaults(t *testing.T, cfg config) {
	t.Helper()
	if cfg.tenantCode != "demo" || cfg.adminUser != "story053_admin" || cfg.graderUser != "test_grader" {
		t.Fatalf("defaults = %#v", cfg)
	}
}

func assertBatch9DatasetModeDefaults(t *testing.T, cfg config, datasetDir string) {
	t.Helper()
	if cfg.aiLimit != 3 || cfg.datasetDir != datasetDir || cfg.resultPath != filepath.Join(datasetDir, "import-result.json") || cfg.aiOnly || cfg.repairCrops || cfg.reconcileOCRRuntime {
		t.Fatalf("defaults = %#v", cfg)
	}
}

func TestBatch9TemplateLayoutContract(t *testing.T) {
	ids, regions := map[string]string{}, map[string]region{}
	for number := 1; number <= 19; number++ {
		question := "Q" + strconv.Itoa(number)
		ids[question] = "question-" + question
		regions[question] = region{X: float64(number) / 100, Y: .2, Width: .3, Height: .4}
	}
	layout := buildTemplateLayout(ids, regions)
	pages := layout["pages"].([]any)
	if len(pages) != 2 {
		t.Fatalf("pages = %d", len(pages))
	}
	for index, rawPage := range pages {
		assertBatch9TemplatePage(t, index, rawPage.(map[string]any), ids, regions)
	}
	profile := layout["omr_profile"].(map[string]any)
	if !reflect.DeepEqual(profile, map[string]any{"mode": "template_difference", "version": "opencv-template-difference-bubble-v1"}) {
		t.Fatalf("profile = %#v", profile)
	}
	logBatch9Proof(t, "template", layout)
}

func assertBatch9TemplatePage(t *testing.T, index int, page map[string]any, ids map[string]string, regions map[string]region) {
	t.Helper()
	if page["page_no"] != index+1 || page["width"] != imageWidth || page["height"] != imageHeight {
		t.Fatalf("page geometry = %#v", page)
	}
	marks := page["registration_marks"].([]any)
	if marks == nil || len(marks) != 0 {
		t.Fatalf("registration marks = %#v", marks)
	}
	questionRegions := page["question_regions"].([]any)
	start, count := 1, 16
	if index == 1 {
		start, count = 17, 3
	}
	if len(questionRegions) != count {
		t.Fatalf("page %d regions = %d", index+1, len(questionRegions))
	}
	for offset, rawRegion := range questionRegions {
		number := start + offset
		question := "Q" + strconv.Itoa(number)
		actual := rawRegion.(map[string]any)
		assertBatch9RegionGeometry(t, question, actual, ids[question], regions[question])
		assertBatch9Options(t, number, question, actual)
	}
}

func assertBatch9RegionGeometry(t *testing.T, question string, actual map[string]any, questionID string, bounds region) {
	t.Helper()
	if actual["id"] != "region-"+strings.ToLower(question) || actual["label"] != question || actual["question_id"] != questionID || actual["x"] != bounds.X || actual["y"] != .2 || actual["width"] != .3 || actual["height"] != .4 {
		t.Fatalf("region %s = %#v", question, actual)
	}
}

func assertBatch9Options(t *testing.T, number int, question string, actual map[string]any) {
	t.Helper()
	if number > 10 {
		if _, exists := actual["option_regions"]; exists {
			t.Fatalf("%s unexpectedly has options", question)
		}
		return
	}
	options := actual["option_regions"].([]any)
	if len(options) != 4 {
		t.Fatalf("%s options = %d", question, len(options))
	}
	for index, label := range []string{"A", "B", "C", "D"} {
		cx := 105 + (number-1)%5*302 + 66 + index*53
		cy := 402 + (number-1)/5*160 + 26
		want := map[string]any{
			"id": strings.ToLower(question) + "-" + strings.ToLower(label), "label": label,
			"x": float64(cx-17) / imageWidth, "y": float64(cy-17) / imageHeight,
			"width": float64(34) / imageWidth, "height": float64(34) / imageHeight,
		}
		if !reflect.DeepEqual(options[index], want) {
			t.Fatalf("%s/%s = %#v, want %#v", question, label, options[index], want)
		}
	}
}

type batch9Transport func(*http.Request) (*http.Response, error)

func (transport batch9Transport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func batch9Client(transport batch9Transport) *apiClient {
	return &apiClient{baseURL: "http://importer.test", token: "token", client: &http.Client{Transport: transport}}
}

func batch9Response(request *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}
}

func TestBatch9UploadProtocol(t *testing.T) {
	tests := []struct {
		name, filename, body, wantError string
		status                          int
	}{
		{name: "success", filename: "nested/crop.png", status: 201, body: `{"file":{"id":"file","hash_sha256":"hash"}}`},
		{name: "empty_filename", filename: " \t", wantError: "upload filename is required"},
		{name: "http_error", filename: "crop.png", status: 409, body: " conflict \n", wantError: "upload crop.png returned 409: conflict"},
		{name: "invalid_json", filename: "crop.png", status: 201, body: "!", wantError: "invalid character"},
		{name: "missing_identity", filename: "crop.png", status: 201, body: `{"file":{"hash_sha256":"hash"}}`, wantError: "upload response lacks file identity/hash"},
		{name: "missing_hash", filename: "crop.png", status: 201, body: `{"file":{"id":"file"}}`, wantError: "upload response lacks file identity/hash"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			client := batch9Client(func(request *http.Request) (*http.Response, error) {
				calls++
				assertBatch9UploadRequest(t, request, test.filename)
				return batch9Response(request, test.status, test.body), nil
			})
			asset, err := client.uploadBytes(context.Background(), test.filename, []byte("png-content"), map[string]string{"owner_type": "answer_segment_crop", "owner_id": "registration", "empty": ""})
			assertBatch9Error(t, err, test.wantError)
			wantCalls := 1
			if test.name == "empty_filename" {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatalf("requests = %d, want %d", calls, wantCalls)
			}
			if test.wantError == "" && !reflect.DeepEqual(asset, map[string]any{"id": "file", "hash_sha256": "hash"}) {
				t.Fatalf("asset = %#v", asset)
			}
			logBatch9Proof(t, test.name, map[string]any{"asset": asset, "error": batch9ErrorText(err), "calls": calls})
		})
	}
}

func assertBatch9UploadRequest(t *testing.T, request *http.Request, sourceFilename string) {
	t.Helper()
	// 先核对认证与上传入口，再消费表单；本地目录不能随文件名上传，空字段也不能进入表单。
	if request.Method != http.MethodPost || request.URL.Path != "/api/v1/files" || request.Header.Get("Accept") != jsonContentType || request.Header.Get("Authorization") != "Bearer token" {
		t.Fatalf("upload request = %#v", request)
	}
	values, filename, content := readBatch9Multipart(t, request)
	if filename != filepath.Base(sourceFilename) || string(content) != "png-content" || !reflect.DeepEqual(values, map[string]string{"owner_type": "answer_segment_crop", "owner_id": "registration"}) {
		t.Fatalf("upload form = %#v %q %q", values, filename, content)
	}
}

func readBatch9Multipart(t *testing.T, request *http.Request) (map[string]string, string, []byte) {
	t.Helper()
	mediaType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || parameters["boundary"] == "" {
		t.Fatalf("multipart type = %s, %v", mediaType, err)
	}
	reader := multipart.NewReader(request.Body, parameters["boundary"])
	values := map[string]string{}
	var filename string
	var content []byte
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		value, readErr := io.ReadAll(part)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if part.FormName() == "file" {
			filename, content = part.FileName(), value
		} else {
			values[part.FormName()] = string(value)
		}
	}
	return values, filename, content
}

type batch9SQLEvent struct {
	Kind  string `json:"kind"`
	Query string `json:"query,omitempty"`
	Args  []any  `json:"args,omitempty"`
}

type batch9SQLScript struct {
	events           []batch9SQLEvent
	queryRows        [][]driver.Value
	queryErr         error
	execErr          error
	beginErr         error
	commitErr        error
	affected         int64
	queryFailAt      int
	queryCount       int
	execCount        int
	queryErrContains string
	importMode       bool
	sequence         *[]string
}

func (script *batch9SQLScript) record(event batch9SQLEvent) {
	script.events = append(script.events, event)
	if script.sequence != nil {
		*script.sequence = append(*script.sequence, "sql:"+event.Kind)
	}
}

type batch9Connector struct{ script *batch9SQLScript }
type batch9Driver struct{}
type batch9Connection struct{ script *batch9SQLScript }
type batch9Transaction struct{ script *batch9SQLScript }
type batch9Rows struct {
	values [][]driver.Value
	index  int
}

func (connector batch9Connector) Connect(context.Context) (driver.Conn, error) {
	return &batch9Connection{script: connector.script}, nil
}
func (batch9Connector) Driver() driver.Driver         { return batch9Driver{} }
func (batch9Driver) Open(string) (driver.Conn, error) { return nil, errors.New("connector required") }
func (*batch9Connection) Close() error                { return nil }
func (*batch9Connection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected Prepare")
}
func (connection *batch9Connection) Begin() (driver.Tx, error) {
	return connection.BeginTx(context.Background(), driver.TxOptions{})
}
func (connection *batch9Connection) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	connection.script.record(batch9SQLEvent{Kind: "begin"})
	if connection.script.beginErr != nil {
		return nil, connection.script.beginErr
	}
	return batch9Transaction{script: connection.script}, nil
}
func (transaction batch9Transaction) Commit() error {
	transaction.script.record(batch9SQLEvent{Kind: "commit"})
	return transaction.script.commitErr
}
func (transaction batch9Transaction) Rollback() error {
	transaction.script.record(batch9SQLEvent{Kind: "rollback"})
	return nil
}
func batch9Args(values []driver.NamedValue) []any {
	args := make([]any, len(values))
	for index, value := range values {
		if bytesValue, ok := value.Value.([]byte); ok {
			args[index] = string(bytesValue)
		} else {
			args[index] = value.Value
		}
	}
	return args
}
func (connection *batch9Connection) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	script := connection.script
	script.record(batch9SQLEvent{Kind: "query", Query: query, Args: batch9Args(args)})
	script.queryCount++
	if script.queryErr != nil && (script.queryFailAt == 0 || script.queryFailAt == script.queryCount) && (script.queryErrContains == "" || strings.Contains(query, script.queryErrContains)) {
		return nil, script.queryErr
	}
	if script.queryRows != nil {
		return &batch9Rows{values: script.queryRows}, nil
	}
	return &batch9Rows{values: [][]driver.Value{{"id-" + strconv.Itoa(script.queryCount)}}}, nil
}
func (connection *batch9Connection) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	script := connection.script
	script.record(batch9SQLEvent{Kind: "exec", Query: query, Args: batch9Args(args)})
	script.execCount++
	if script.execErr != nil {
		return nil, script.execErr
	}
	if script.importMode && strings.Contains(query, "UPDATE question") {
		return driver.RowsAffected(19), nil
	}
	return driver.RowsAffected(script.affected), nil
}
func (rows *batch9Rows) Columns() []string {
	count := 1
	if len(rows.values) > 0 {
		count = len(rows.values[0])
	}
	columns := make([]string, count)
	for index := range columns {
		columns[index] = strconv.Itoa(index)
	}
	return columns
}
func (*batch9Rows) Close() error { return nil }
func (rows *batch9Rows) Next(values []driver.Value) error {
	if rows.index == len(rows.values) {
		return io.EOF
	}
	copy(values, rows.values[rows.index])
	rows.index++
	return nil
}
func openBatch9DB(t *testing.T, script *batch9SQLScript) *sql.DB {
	t.Helper()
	db := sql.OpenDB(batch9Connector{script: script})
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestBatch9RegistrationTransactions(t *testing.T) {
	tests := []struct {
		name, wantError string
		failAt          int
		commitErr       error
	}{
		{name: "success"},
		{name: "capture_file_failure", failAt: 1, wantError: "create capture file C001 page 2: injected"},
		{name: "capture_page_failure", failAt: 2, wantError: "create capture page C001 page 2: injected"},
		{name: "registration_failure", failAt: 3, wantError: "create registration C001 page 2: injected"},
		{name: "commit_failure", commitErr: errors.New("commit failed"), wantError: "commit failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := &batch9SQLScript{commitErr: test.commitErr}
			if test.failAt > 0 {
				script.queryErr, script.queryFailAt = errors.New("injected"), test.failAt
			}
			db := openBatch9DB(t, script)
			id, err := createBatch9Registration(context.Background(), db, pageRecord{
				ID: "page", PageNo: 2, Path: "nested/page.png",
				FileAsset: map[string]any{"id": "source", "hash_sha256": "source-hash", "size_bytes": float64(321)},
			})
			assertBatch9Error(t, err, test.wantError)
			if test.wantError == "" && id != "id-3" || test.wantError != "" && id != "" {
				t.Fatalf("registration id = %q", id)
			}
			assertBatch9RegistrationTransaction(t, script, test.failAt)
			logBatch9Proof(t, test.name, map[string]any{"id": id, "error": batch9ErrorText(err), "sql": script.events})
		})
	}
}

func assertBatch9RegistrationTransaction(t *testing.T, script *batch9SQLScript, failAt int) {
	t.Helper()
	// 查询失败必须回滚；提交失败仍应留下已经尝试提交的调用事实。
	wantEnd := "commit"
	if failAt > 0 {
		wantEnd = "rollback"
	}
	if script.events[0].Kind != "begin" || script.events[len(script.events)-1].Kind != wantEnd {
		t.Fatalf("transaction = %#v", script.events)
	}
	wantArgs := []any{"tenant", "batch", "source", "page.png", "source-hash", int64(321), datasetID + ":C001:page:2", "admin"}
	if !reflect.DeepEqual(script.events[1].Args, wantArgs) {
		t.Fatalf("capture file bindings = %#v", script.events[1].Args)
	}
}

func TestBatch9RegistrationCompletion(t *testing.T) {
	for _, affected := range []int64{0, 1, 2} {
		t.Run(strconv.FormatInt(affected, 10), func(t *testing.T) {
			script := &batch9SQLScript{affected: affected}
			err := completeBatch9Registration(context.Background(), openBatch9DB(t, script), map[string]any{"id": "registered"})
			wantError, wantCount, wantEnd := "", 2, "commit"
			if affected != 1 {
				wantError, wantCount, wantEnd = fmt.Sprintf("complete registration affected %d rows", affected), 1, "rollback"
			}
			assertBatch9Error(t, err, wantError)
			if script.execCount != wantCount || script.events[len(script.events)-1].Kind != wantEnd || !reflect.DeepEqual(script.events[1].Args, []any{"tenant", "registration", "registered"}) {
				t.Fatalf("completion = %#v", script.events)
			}
			if affected == 1 && !reflect.DeepEqual(script.events[2].Args, []any{"tenant", "submission", "page", "template", "template-hash", "registration", int64(imageWidth), int64(imageHeight)}) {
				t.Fatalf("segment evidence bindings = %#v", script.events[2].Args)
			}
			logBatch9Proof(t, "completion", map[string]any{"error": batch9ErrorText(err), "sql": script.events})
		})
	}
}

func TestBatch9AnswerCandidateDecisions(t *testing.T) {
	tests := []struct {
		name, route, extractor, decision string
		text                             any
	}{
		{name: "automatic_blank_still_confirmed", route: "auto_confirm", extractor: "omr", decision: "confirmed", text: ""},
		{name: "blank", extractor: "ocr", decision: "blank", text: " \t"},
		{name: "multiple", extractor: "omr", decision: "multiple", text: []any{"A", "C"}},
		{name: "one_selection", extractor: "omr", decision: "ambiguous", text: []any{"A"}},
		{name: "typed_strings_remain_ambiguous", extractor: "ocr", decision: "ambiguous", text: []string{"A", "C"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := &batch9SQLScript{}
			id, err := createBatch9AnswerCandidate(context.Background(), openBatch9DB(t, script), candidateAnswer{Text: test.text, Confidence: .88, Route: test.route, Synthetic: true}, test.extractor)
			if err != nil || id != "id-1" || len(script.events) != 1 {
				t.Fatalf("candidate = %q %v %#v", id, err, script.events)
			}
			args := script.events[0].Args
			if args[0] != "tenant" || args[1] != "segment" || args[2] != test.extractor || args[4] != displayText(test.text) || args[5] != .88 || args[6] != test.decision || args[11] != "admin" {
				t.Fatalf("candidate bindings = %#v", args)
			}
			logBatch9Proof(t, test.name, map[string]any{"id": id, "sql": script.events})
		})
	}
}

func TestBatch9ObjectiveGradeBindings(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(strconv.FormatBool(failed), func(t *testing.T) {
			script := &batch9SQLScript{affected: 1}
			wantError := ""
			if failed {
				script.execErr, wantError = errors.New("injected"), "confirm automatic objective grade: injected"
			}
			grade := map[string]any{"id": "grade", "grader_type": "rule", "rule_version": "v1", "confidence": .99, "risk_flags": []any{}, "suggested_score": 3.0, "max_score": 4.0}
			err := confirmBatch9ObjectiveGrade(context.Background(), openBatch9DB(t, script), grade)
			assertBatch9Error(t, err, wantError)
			args := script.events[0].Args
			if !reflect.DeepEqual(args[:9], []any{"tenant", "exam", "submission", "question", "segment", "candidate", "rule", 3.0, 4.0}) || args[10] != "admin" {
				t.Fatalf("grade bindings = %#v", args)
			}
			logBatch9Proof(t, "grade", map[string]any{"error": batch9ErrorText(err), "sql": script.events})
		})
	}
}

func batch9DatasetFixture(t *testing.T) (config, dataset) {
	t.Helper()
	dir := t.TempDir()
	var content bytes.Buffer
	if err := png.Encode(&content, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"page1.png", "page2.png", "blank.pdf", "questions.pdf", "answers.pdf"} {
		if err := os.WriteFile(filepath.Join(dir, name), content.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	definitions, regions := []questionDef{}, map[string]region{}
	for number := 1; number <= 19; number++ {
		question := "Q" + strconv.Itoa(number)
		definitions = append(definitions, questionDef{QuestionNo: question, QuestionType: "single_choice", Score: 4, Stem: "synthetic", StandardAnswer: "A"})
		regions[question] = region{X: .1, Y: .2, Width: .3, Height: .4}
	}
	return config{datasetDir: dir, graderUser: "teacher", aiLimit: 3}, dataset{
		Manifest:  manifest{Files: map[string]any{"blank_answer_sheet": "blank.pdf", "official_questions": "questions.pdf", "official_answers": "answers.pdf"}},
		Questions: definitions,
		Candidates: []candidateDef{
			{CandidateNo: "C001", PageFiles: []string{"page1.png", "page2.png"}, Synthetic: true},
			{CandidateNo: "C001", PageFiles: []string{"page1.png", "page2.png"}, Synthetic: true},
		},
		Regions: map[string]map[string]region{"C001": regions},
	}
}

type batch9HTTPEvent struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   any    `json:"body,omitempty"`
}

type batch9ImportHTTP struct {
	t             *testing.T
	mode          string
	questions     []questionDef
	events        []batch9HTTPEvent
	sequence      *[]string
	students      int
	questionCount int
	assetCount    int
	pageCount     int
}

func (fixture *batch9ImportHTTP) roundTrip(request *http.Request) (*http.Response, error) {
	event := batch9HTTPEvent{Method: request.Method, Path: request.URL.Path}
	if strings.HasPrefix(request.Header.Get("Content-Type"), "multipart/") {
		values, name, content := readBatch9Multipart(fixture.t, request)
		event.Body = map[string]any{"fields": values, "filename": name, "sha256": sha256Hex(content)}
	} else if request.Body != nil {
		if err := json.NewDecoder(request.Body).Decode(&event.Body); err != nil {
			fixture.t.Fatal(err)
		}
	}
	fixture.events = append(fixture.events, event)
	*fixture.sequence = append(*fixture.sequence, request.Method+":"+request.URL.Path)
	status, body := fixture.response(request)
	return batch9Response(request, status, body), nil
}

func (fixture *batch9ImportHTTP) response(request *http.Request) (int, string) {
	path := request.URL.Path
	switch {
	case path == "/api/v1/schools":
		return 201, `{"school":{"id":"school"}}`
	case path == "/api/v1/grades":
		return 201, `{"grade":{"id":"grade"}}`
	case path == "/api/v1/classes":
		return 201, `{"class":{"id":"class"}}`
	case path == "/api/v1/students":
		fixture.students++
		return 201, fmt.Sprintf(`{"student":{"id":"student-%d"}}`, fixture.students)
	case path == "/api/v1/exams":
		return 201, `{"exam":{"id":"exam"}}`
	case path == "/api/v1/files":
		fixture.assetCount++
		return 201, fmt.Sprintf(`{"file":{"id":"asset-%d","hash_sha256":"hash-%d","size_bytes":321}}`, fixture.assetCount, fixture.assetCount)
	case strings.HasSuffix(path, "/papers"):
		return 201, `{"paper":{"id":"paper"}}`
	case strings.HasSuffix(path, "/questions"):
		fixture.questionCount++
		return 201, fmt.Sprintf(`{"question":{"id":"question-%d"}}`, fixture.questionCount)
	case strings.HasSuffix(path, "/scoring-rules"):
		return 201, fmt.Sprintf(`{"scoring_rule":{"id":"rule-%d"}}`, fixture.questionCount)
	case strings.HasSuffix(path, "/publish"):
		if fixture.mode == "publish_failure" {
			return 500, `{"error":"injected"}`
		}
		return 200, `{}`
	case strings.HasSuffix(path, "/rubric"):
		return 201, fmt.Sprintf(`{"rubric":{"id":"rubric-%d"}}`, fixture.questionCount)
	case strings.HasSuffix(path, "/answer-sheet-templates"):
		return 201, `{"template":{"id":"template"}}`
	case strings.HasSuffix(path, "/lock"):
		if fixture.mode == "lock_failure" {
			return 500, `{"error":"injected"}`
		}
		return 200, `{"template":{"content_hash":"template-hash"}}`
	case strings.HasSuffix(path, "/readiness/confirm"), strings.HasSuffix(path, "/start-collection"):
		return 200, `{}`
	case strings.HasSuffix(path, "/submissions"):
		return 201, `{"submission":{"id":"submission"}}`
	case strings.HasSuffix(path, "/pages"):
		fixture.pageCount++
		return 201, fmt.Sprintf(`{"page":{"id":"page-%d"}}`, fixture.pageCount)
	case strings.HasSuffix(path, "/quality-check"):
		if fixture.mode == "integrity_failure" {
			return 200, `{"result":{"valid":false,"issues":["missing_page"]}}`
		}
		return 200, `{"result":{"valid":true}}`
	case strings.HasSuffix(path, "/run-quality-check"):
		return 202, `{}`
	case request.Method == http.MethodGet && path == "/api/v1/submissions/submission":
		return 200, `{"submission":{"quality_status":"passed"}}`
	case strings.HasSuffix(path, "/status"):
		return 200, `{}`
	case strings.HasSuffix(path, "/segment-answers"):
		return fixture.segmentResponse()
	default:
		fixture.t.Fatalf("unexpected HTTP request %s %s", request.Method, path)
		return 500, `{}`
	}
}

func (fixture *batch9ImportHTTP) segmentResponse() (int, string) {
	segments := []any{}
	for _, question := range fixture.questions {
		segments = append(segments, map[string]any{"question_no": question.QuestionNo, "id": "segment-" + question.QuestionNo})
	}
	if fixture.mode == "segment_count_failure" {
		segments = segments[:len(segments)-1]
	}
	body, err := json.Marshal(map[string]any{"result": map[string]any{"segments": segments}})
	if err != nil {
		fixture.t.Fatal(err)
	}
	return 200, string(body)
}

func TestBatch9ImportStageOrderAndFailureResult(t *testing.T) {
	// 在 OCR 原生事务入口注入失败，核对此前配准与裁剪顺序，并拒绝返回半成品汇总。
	tests := []struct{ name, wantError string }{
		{"missing_region", "missing region for Q1"},
		{"publish_failure", "publish rule for Q1:"},
		{"lock_failure", "lock answer sheet template:"},
		{"integrity_failure", "collection integrity failed for C001: [missing_page]"},
		{"segment_count_failure", "C001 generated 18 segments, want 19"},
		{"ocr_boundary", "atomically seed OCR task for C001: stop before OCR"},
		{"question_order", "atomically seed OCR task for C001: stop before OCR"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, data := prepareBatch9ImportFailureDataset(t, test.name)
			sequence := []string{}
			fixture := &batch9ImportHTTP{t: t, mode: test.name, questions: data.Questions, sequence: &sequence}
			script := &batch9SQLScript{
				affected: 1, importMode: true, sequence: &sequence,
				queryErr: errors.New("stop before OCR"), queryErrContains: "INSERT INTO ocr_task",
			}
			result, err := runBatch9Dataset(context.Background(), cfg, data, batch9Client(fixture.roundTrip), openBatch9DB(t, script))
			assertBatch9Error(t, err, test.wantError)
			if !reflect.DeepEqual(result, importResult{}) {
				t.Fatalf("failed import returned partial result: %#v", result)
			}
			assertBatch9ImportRosterOrder(t, fixture.events)
			assertBatch9CandidateBindings(t, fixture.events)
			if test.name == "ocr_boundary" || test.name == "question_order" {
				assertBatch9ScoringDefinitions(t, fixture.events, data.Questions)
				if script.execCount != 24 || script.queryCount != 8 || script.events[len(script.events)-1].Kind != "rollback" {
					t.Fatalf("pre-OCR SQL = %#v", script.events)
				}
			}
			logBatch9Proof(t, test.name, map[string]any{"error": batch9ErrorText(err), "result": result, "http": fixture.events, "sql": script.events, "sequence": sequence})
		})
	}
}

func prepareBatch9ImportFailureDataset(t *testing.T, name string) (config, dataset) {
	t.Helper()
	cfg, data := batch9DatasetFixture(t)
	// 故障输入必须在记录 HTTP/SQL 事件前设置；交换题序用来核对导入沿用数据集顺序。
	if name == "missing_region" {
		delete(data.Regions["C001"], "Q1")
	}
	if name == "question_order" {
		data.Questions[0], data.Questions[16] = data.Questions[16], data.Questions[0]
	}
	return cfg, data
}

func assertBatch9ImportRosterOrder(t *testing.T, events []batch9HTTPEvent) {
	t.Helper()
	if len(events) < 8 || events[0].Path != "/api/v1/schools" || events[1].Path != "/api/v1/grades" || events[2].Path != "/api/v1/classes" || events[3].Path != "/api/v1/students" || events[4].Path != "/api/v1/students" || events[5].Path != "/api/v1/exams" {
		t.Fatalf("roster order = %#v", events)
	}
}

func assertBatch9CandidateBindings(t *testing.T, events []batch9HTTPEvent) {
	t.Helper()
	for _, event := range events {
		if strings.HasSuffix(event.Path, "/submissions") {
			body := event.Body.(map[string]any)
			if body["student_id"] != "student-2" || body["candidate_no"] != "C001" || body["expected_page_count"] != float64(2) {
				t.Fatalf("duplicate candidate binding = %#v", body)
			}
		}
		if strings.HasSuffix(event.Path, "/status") {
			if !reflect.DeepEqual(event.Body, map[string]any{"status": "ready_for_ocr", "expected_revision": float64(1)}) {
				t.Fatalf("ready transition = %#v", event.Body)
			}
		}
	}
}

func assertBatch9ScoringDefinitions(t *testing.T, events []batch9HTTPEvent, questions []questionDef) {
	t.Helper()
	rules, rubrics, ordinal := 0, 0, 0
	for _, event := range events {
		if strings.HasSuffix(event.Path, "/questions") {
			assertBatch9QuestionOrder(t, event, questions[ordinal], ordinal)
			ordinal++
		}
		if strings.HasSuffix(event.Path, "/scoring-rules") {
			rules++
			assertBatch9ObjectiveRuleRoute(t, event, rules)
		}
		if strings.HasSuffix(event.Path, "/rubric") {
			rubrics++
			assertBatch9SubjectiveRubricRoute(t, event, rubrics)
		}
	}
	if rules != 16 || rubrics != 3 || ordinal != 19 {
		t.Fatalf("definitions = %d/%d/%d", rules, rubrics, ordinal)
	}
}

func assertBatch9QuestionOrder(t *testing.T, event batch9HTTPEvent, question questionDef, ordinal int) {
	t.Helper()
	body := event.Body.(map[string]any)
	if body["question_no"] != question.QuestionNo || body["sort_order"] != float64(ordinal+1) {
		t.Fatalf("question order = %#v", body)
	}
}

func assertBatch9ObjectiveRuleRoute(t *testing.T, event batch9HTTPEvent, rules int) {
	t.Helper()
	if rules > 16 || event.Path != fmt.Sprintf("/api/v1/questions/question-%d/scoring-rules", rules) {
		t.Fatalf("rule route = %s", event.Path)
	}
}

func assertBatch9SubjectiveRubricRoute(t *testing.T, event batch9HTTPEvent, rubrics int) {
	t.Helper()
	if event.Path != fmt.Sprintf("/api/v1/questions/question-%d/rubric", 16+rubrics) {
		t.Fatalf("rubric route = %s", event.Path)
	}
}

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
