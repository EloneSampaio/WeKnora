package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

type paperlessManualSourceFake struct {
	interfaces.DataSourceService
	ds            *types.DataSource
	getCalls      int
	mutationCalls int
}

func (f *paperlessManualSourceFake) GetDataSource(_ context.Context, id string) (*types.DataSource, error) {
	f.getCalls++
	if id != f.ds.ID {
		return nil, errors.New("unexpected data source id")
	}
	return f.ds, nil
}

func (f *paperlessManualSourceFake) UpdateDataSource(context.Context, *types.DataSource) (*types.DataSource, error) {
	f.mutationCalls++
	return f.ds, nil
}

func (f *paperlessManualSourceFake) UpdateDataSourceCredentials(context.Context, string, map[string]interface{}) (*types.DataSource, error) {
	f.mutationCalls++
	return f.ds, nil
}

func (f *paperlessManualSourceFake) ClearDataSourceCredentials(context.Context, string) error {
	f.mutationCalls++
	return nil
}

func (f *paperlessManualSourceFake) ManualSync(context.Context, string) (*types.SyncLog, error) {
	f.mutationCalls++
	return nil, nil
}

type paperlessManualKBFake struct {
	interfaces.KnowledgeBaseService
	kb       *types.KnowledgeBase
	getCalls int
}

func (f *paperlessManualKBFake) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	f.getCalls++
	if id != f.kb.ID {
		return nil, errors.New("unexpected knowledge base id")
	}
	return f.kb, nil
}

type paperlessManualFileCall struct {
	KBID        string
	Filename    string
	Metadata    map[string]string
	Channel     string
	Bytes       []byte
	EnableMulti *bool
	Tags        []string
	Process     *types.KnowledgeProcessOverrides
}

type paperlessManualKnowledgeFake struct {
	interfaces.KnowledgeService
	calls     []paperlessManualFileCall
	createErr error
}

func (f *paperlessManualKnowledgeFake) CreateKnowledgeFromFile(
	_ context.Context,
	kbID string,
	file *multipart.FileHeader,
	metadata map[string]string,
	enableMultimodel *bool,
	customFileName string,
	tagIDs []string,
	channel string,
	processOverrides *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	opened, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer opened.Close()
	body, err := io.ReadAll(opened)
	if err != nil {
		return nil, err
	}
	f.calls = append(f.calls, paperlessManualFileCall{
		KBID: kbID, Filename: customFileName, Metadata: cloneManualMetadata(metadata), Channel: channel,
		Bytes: body, EnableMulti: enableMultimodel, Tags: append([]string(nil), tagIDs...), Process: processOverrides,
	})
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &types.Knowledge{ID: fmt.Sprintf("knowledge-%d", len(f.calls))}, nil
}

func cloneManualMetadata(source map[string]string) map[string]string {
	out := make(map[string]string, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func paperlessManualServiceFixture(t *testing.T, handler http.HandlerFunc) (*PaperlessManualService, *paperlessManualSourceFake, *paperlessManualKBFake, *paperlessManualKnowledgeFake, *httptest.Server) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,::1")
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token manual-test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler(w, r)
	}))
	ds := &types.DataSource{
		ID: "paperless-connection", TenantID: 7, KnowledgeBaseID: "kb-7", Type: types.ConnectorTypePaperless,
		SyncMode: types.SyncModeManual, Status: types.DataSourceStatusPaused, SyncDeletions: false,
	}
	config, err := json.Marshal(types.DataSourceConfig{
		Type:        types.ConnectorTypePaperless,
		Credentials: map[string]interface{}{"base_url": server.URL, "api_token": "manual-test-token"},
		Settings:    map[string]interface{}{}, ResourceIDs: []string{},
	})
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	ds.Config = types.JSON(config)
	sources := &paperlessManualSourceFake{ds: ds}
	bases := &paperlessManualKBFake{kb: &types.KnowledgeBase{ID: ds.KnowledgeBaseID, TenantID: ds.TenantID}}
	knowledge := &paperlessManualKnowledgeFake{}
	return NewPaperlessManualService(sources, bases, knowledge), sources, bases, knowledge, server
}

func paperlessManualContext(tenantID uint64, role types.TenantRole) context.Context {
	return types.WithCaller(context.Background(), types.Caller{TenantID: tenantID, UserID: "manual-user", Role: role})
}

func paperlessManualImportResponse(t *testing.T, data []byte, name string, status int) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if status != 0 {
			http.Error(w, http.StatusText(status), status)
			return
		}
		switch r.URL.Path {
		case "/api/documents/":
			http.Error(w, "unexpected sync list request", http.StatusBadRequest)
		case "/api/documents/42/":
			if r.Method != http.MethodGet {
				t.Errorf("metadata method = %s", r.Method)
			}
			body, _ := json.Marshal(map[string]interface{}{"id": 42, "title": "Invoice", "original_file_name": name})
			_, _ = w.Write(body)
		case "/api/documents/42/download/":
			if r.URL.Query().Get("original") != "true" {
				t.Errorf("download query = %q, want original=true", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write(data)
		default:
			t.Errorf("unexpected Paperless request %s", r.URL.String())
			http.NotFound(w, r)
		}
	}
}

func TestPaperlessManualImportUsesNativeFileIngestionAndOnlySelectedIDs(t *testing.T) {
	originals := map[int][]byte{42: []byte("%PDF-original-42"), 43: []byte("%PDF-original-43")}
	requests := make([]string, 0, 4)
	service, sources, bases, knowledge, server := paperlessManualServiceFixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		var id int
		if _, err := fmt.Sscanf(r.URL.Path, "/api/documents/%d/", &id); err == nil && originals[id] != nil {
			if strings.HasSuffix(r.URL.Path, "/download/") {
				if r.URL.Query().Get("original") != "true" {
					t.Errorf("original query missing: %s", r.URL.RawQuery)
				}
				w.Header().Set("Content-Type", "application/pdf")
				_, _ = w.Write(originals[id])
				return
			}
			_, _ = fmt.Fprintf(w, `{"id":%d,"title":"Invoice %d","original_file_name":"invoice-%d.pdf"}`, id, id, id)
			return
		}
		t.Errorf("unexpected Paperless request %s", r.URL.String())
		http.NotFound(w, r)
	})
	defer server.Close()
	before := *sources.ds

	result, err := service.Import(paperlessManualContext(7, types.TenantRoleAdmin), sources.ds.ID, PaperlessImportRequest{
		DocumentIDs: []int{42, 43, 42}, FolderPath: "Paperless/2026",
	})
	if err != nil {
		t.Fatalf("Import error: %v", err)
	}
	if len(result.Results) != 2 || result.Results[0].DocumentID != 42 || result.Results[1].DocumentID != 43 {
		t.Fatalf("import results = %+v", result.Results)
	}
	if len(knowledge.calls) != 2 {
		t.Fatalf("native file ingestions = %d, want 2", len(knowledge.calls))
	}
	for i, id := range []int{42, 43} {
		call := knowledge.calls[i]
		wantName := fmt.Sprintf("Paperless/2026/invoice-%d.pdf", id)
		if call.KBID != "kb-7" || call.Filename != wantName || call.Channel != types.ConnectorTypePaperless || !bytes.Equal(call.Bytes, originals[id]) {
			t.Errorf("native ingestion %d = %+v", id, call)
		}
		wantMetadata := map[string]string{
			"source": "paperless", "paperless_document_id": fmt.Sprint(id),
			"paperless_connection_id": sources.ds.ID,
			"source_url":              fmt.Sprintf("%s/documents/%d/", server.URL, id),
			"original_filename":       fmt.Sprintf("invoice-%d.pdf", id),
		}
		if !reflect.DeepEqual(call.Metadata, wantMetadata) {
			t.Errorf("metadata for %d = %#v, want %#v", id, call.Metadata, wantMetadata)
		}
	}
	wantRequests := []string{
		"GET /api/documents/42/", "GET /api/documents/42/download/",
		"GET /api/documents/43/", "GET /api/documents/43/download/",
	}
	if !reflect.DeepEqual(requests, wantRequests) {
		t.Fatalf("upstream requests = %v, want selected docs once: %v", requests, wantRequests)
	}
	if sources.getCalls != 1 || sources.mutationCalls != 0 || bases.getCalls != 1 {
		t.Fatalf("source/KB calls: get source=%d mutations=%d get KB=%d", sources.getCalls, sources.mutationCalls, bases.getCalls)
	}
	if !reflect.DeepEqual(*sources.ds, before) {
		t.Fatal("import mutated persisted datasource fields")
	}
}

func TestPaperlessManualImportDuplicateAndNativePipelineError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		dup  bool
	}{
		{name: "duplicate", err: types.NewDuplicateFileError(&types.Knowledge{FileName: "existing.pdf"}), dup: true},
		{name: "native pipeline error", err: errors.New("unsupported native parser"), dup: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, sources, _, knowledge, server := paperlessManualServiceFixture(t, paperlessManualImportResponse(t, []byte("%PDF-original"), "original.pdf", 0))
			defer server.Close()
			knowledge.createErr = tc.err
			result, err := service.Import(paperlessManualContext(7, types.TenantRoleAdmin), sources.ds.ID, PaperlessImportRequest{DocumentIDs: []int{42}})
			if err != nil || len(result.Results) != 1 {
				t.Fatalf("Import = %+v, %v", result, err)
			}
			if result.Results[0].Duplicate != tc.dup || (tc.dup && result.Results[0].Error != "") || (!tc.dup && !strings.Contains(result.Results[0].Error, "unsupported native parser")) {
				t.Fatalf("result = %+v", result.Results[0])
			}
		})
	}
}

func TestPaperlessManualImportUpstreamFailuresAndSizeCap(t *testing.T) {
	tests := []struct {
		name       string
		failPath   string
		status     int
		maxSizeMB  string
		body       []byte
		wantCreate int
	}{
		{name: "upstream unauthorized metadata", failPath: "/api/documents/42/", status: http.StatusUnauthorized},
		{name: "missing metadata", failPath: "/api/documents/42/", status: http.StatusNotFound},
		{name: "download failure", failPath: "/api/documents/42/download/", status: http.StatusServiceUnavailable},
		{name: "upload size cap", maxSizeMB: "1", body: bytes.Repeat([]byte("x"), 1024*1024+1)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.maxSizeMB != "" {
				t.Setenv("MAX_FILE_SIZE_MB", tc.maxSizeMB)
			} else {
				t.Setenv("MAX_FILE_SIZE_MB", "1")
			}
			service, sources, _, knowledge, server := paperlessManualServiceFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == tc.failPath {
					http.Error(w, http.StatusText(tc.status), tc.status)
					return
				}
				if tc.failPath == "" && strings.HasSuffix(r.URL.Path, "/download/") {
					w.Header().Set("Content-Type", "application/pdf")
					_, _ = w.Write(tc.body)
					return
				}
				if r.URL.Path == "/api/documents/42/" {
					_, _ = io.WriteString(w, `{"id":42,"title":"Large","original_file_name":"large.pdf"}`)
					return
				}
				http.NotFound(w, r)
			})
			defer server.Close()
			result, err := service.Import(paperlessManualContext(7, types.TenantRoleAdmin), sources.ds.ID, PaperlessImportRequest{DocumentIDs: []int{42}})
			if err != nil || len(result.Results) != 1 || result.Results[0].Error == "" {
				t.Fatalf("expected per-document failure, got result=%+v err=%v", result, err)
			}
			if len(knowledge.calls) != tc.wantCreate {
				t.Fatalf("native ingestion calls = %d, want %d; result=%+v", len(knowledge.calls), tc.wantCreate, result.Results[0])
			}
		})
	}
}

func TestPaperlessManualImportAuthorizationBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		tenant   uint64
		role     types.TenantRole
		key      *types.TenantAPIKeyScope
		kbTenant uint64
		wantErr  error
	}{
		{name: "cross tenant source", tenant: 8, role: types.TenantRoleAdmin, kbTenant: 7, wantErr: access.ErrForbidden},
		{name: "tenant viewer", tenant: 7, role: types.TenantRoleViewer, kbTenant: 7, wantErr: access.ErrForbidden},
		{name: "manage only key cannot ingest", tenant: 7, role: types.TenantRoleAdmin, kbTenant: 7, key: &types.TenantAPIKeyScope{Capabilities: types.StringArray{string(types.APIKeyCapabilityManageDataSources)}, KnowledgeBaseIDs: types.StringArray{"kb-7"}}, wantErr: access.ErrForbidden},
		{name: "key scoped to another KB", tenant: 7, role: types.TenantRoleAdmin, kbTenant: 7, key: &types.TenantAPIKeyScope{Capabilities: types.StringArray{string(types.APIKeyCapabilityManageDataSources), string(types.APIKeyCapabilityIngest)}, KnowledgeBaseIDs: types.StringArray{"kb-other"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service, sources, bases, knowledge, server := paperlessManualServiceFixture(t, func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unauthorized request reached Paperless: %s", r.URL.String())
				http.Error(w, "not expected", http.StatusInternalServerError)
			})
			defer server.Close()
			bases.kb.TenantID = tc.kbTenant
			ctx := paperlessManualContext(tc.tenant, tc.role)
			if tc.key != nil {
				ctx = types.WithTenantAPIKeyScope(ctx, *tc.key)
			}
			_, err := service.Import(ctx, sources.ds.ID, PaperlessImportRequest{DocumentIDs: []int{42}})
			if (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) || (tc.wantErr == nil && err == nil) {
				t.Fatalf("Import error = %v, want %v", err, tc.wantErr)
			}
			if len(knowledge.calls) != 0 {
				t.Fatalf("unauthorized native ingestion calls = %d", len(knowledge.calls))
			}
		})
	}
}

func TestPaperlessManualImportScopedKeyNeedsIngestAndKBScope(t *testing.T) {
	service, sources, _, knowledge, server := paperlessManualServiceFixture(t, paperlessManualImportResponse(t, []byte("%PDF-scoped"), "scoped.pdf", 0))
	defer server.Close()
	ctx := paperlessManualContext(7, types.TenantRoleAdmin)
	ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{
		Capabilities:     types.StringArray{string(types.APIKeyCapabilityManageDataSources), string(types.APIKeyCapabilityIngest)},
		KnowledgeBaseIDs: types.StringArray{"kb-7"},
	})
	result, err := service.Import(ctx, sources.ds.ID, PaperlessImportRequest{DocumentIDs: []int{42}})
	if err != nil || len(result.Results) != 1 || result.Results[0].Error != "" {
		t.Fatalf("scoped import result=%+v err=%v", result, err)
	}
	if len(knowledge.calls) != 1 || !bytes.Equal(knowledge.calls[0].Bytes, []byte("%PDF-scoped")) {
		t.Fatalf("scoped key native calls = %+v", knowledge.calls)
	}
}

func TestPaperlessManualImportRejectsInvalidSelectionsBeforeIO(t *testing.T) {
	service, sources, _, knowledge, server := paperlessManualServiceFixture(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("invalid selection reached Paperless: %s", r.URL.String())
		http.Error(w, "not expected", http.StatusInternalServerError)
	})
	defer server.Close()
	tooMany := make([]int, 101)
	for i := range tooMany {
		tooMany[i] = i + 1
	}
	for _, ids := range [][]int{nil, []int{0}, tooMany} {
		_, err := service.Import(paperlessManualContext(7, types.TenantRoleAdmin), sources.ds.ID, PaperlessImportRequest{DocumentIDs: ids})
		if err == nil {
			t.Fatalf("Import accepted %d IDs", len(ids))
		}
	}
	if sources.getCalls != 0 || len(knowledge.calls) != 0 {
		t.Fatalf("invalid request performed I/O: source gets=%d creates=%d", sources.getCalls, len(knowledge.calls))
	}
}

func TestPaperlessManualImportUnauthenticatedAndForeignKB(t *testing.T) {
	service, sources, bases, knowledge, server := paperlessManualServiceFixture(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("denied request reached Paperless: %s", r.URL.String())
		http.Error(w, "not expected", http.StatusInternalServerError)
	})
	defer server.Close()
	_, err := service.Import(context.Background(), sources.ds.ID, PaperlessImportRequest{DocumentIDs: []int{42}})
	if !errors.Is(err, access.ErrUnauthorized) {
		t.Fatalf("unauthenticated error = %v", err)
	}
	bases.kb.TenantID = 8
	_, err = service.Import(paperlessManualContext(7, types.TenantRoleAdmin), sources.ds.ID, PaperlessImportRequest{DocumentIDs: []int{42}})
	if !errors.Is(err, access.ErrForbidden) {
		t.Fatalf("foreign KB error = %v", err)
	}
	if len(knowledge.calls) != 0 {
		t.Fatalf("denied native ingestion calls = %d", len(knowledge.calls))
	}
}

func TestPaperlessManualImportDataSourceCredentialFailure(t *testing.T) {
	service, sources, _, knowledge, server := paperlessManualServiceFixture(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
	defer server.Close()
	var cfg types.DataSourceConfig
	if err := json.Unmarshal(sources.ds.Config, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Credentials["api_token"] = "wrong"
	encoded, _ := json.Marshal(cfg)
	sources.ds.Config = types.JSON(encoded)
	result, err := service.Import(paperlessManualContext(7, types.TenantRoleAdmin), sources.ds.ID, PaperlessImportRequest{DocumentIDs: []int{42}})
	if err != nil || len(result.Results) != 1 || !strings.Contains(result.Results[0].Error, "status 401") {
		t.Fatalf("credential failure result=%+v err=%v", result, err)
	}
	if len(knowledge.calls) != 0 {
		t.Fatalf("credential failure native calls = %d", len(knowledge.calls))
	}
}
