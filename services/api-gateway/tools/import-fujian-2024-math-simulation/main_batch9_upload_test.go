package main

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

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

func (batch9Connector) Driver() driver.Driver { return batch9Driver{} }

func (batch9Driver) Open(string) (driver.Conn, error) { return nil, errors.New("connector required") }

func (*batch9Connection) Close() error { return nil }

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
