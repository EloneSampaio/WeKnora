package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/datasource/connector/paperless"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

func TestPaperlessManualModeCannotSubscribe(t *testing.T) {
	for _, tc := range []struct {
		name       string
		sourceType string
		schedule   string
		config     string
	}{
		{"schedule", "paperless", "0 0 * * * *", `{"settings":{}}`},
		{"resource", "paperless", "", `{"resource_ids":["correspondent:1"]}`},
		{"filter", "paperless", "", `{"settings":{"include_archived":true}}`},
		{"other connector", "rss", "", `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := &types.DataSource{SyncMode: types.SyncModeManual, Type: tc.sourceType, SyncSchedule: tc.schedule, Config: []byte(tc.config)}
			require.Error(t, validateManualDataSource(ds))
		})
	}
}

func TestPaperlessManualConnectionLifecycle(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,::1")
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	f := newSQLiteDataSourceDeleteFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Token synthetic", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[],"next":null}`))
	}))
	defer server.Close()
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(paperless.NewConnector()))
	svc := &DataSourceService{dsRepo: f.dsRepo, syncLogRepo: f.syncLogRepo, scheduler: f.scheduler, connectorRegistry: registry,
		kbService: &processSyncKBService{kb: &types.KnowledgeBase{ID: f.ds.KnowledgeBaseID, TenantID: 1}}}
	cfg := &types.DataSourceConfig{Type: "paperless", Credentials: map[string]interface{}{"base_url": server.URL, "api_token": "synthetic"}}
	blob, err := cfg.ToJSON()
	require.NoError(t, err)
	ds, err := svc.CreateDataSource(context.Background(), &types.DataSource{ID: "manual", TenantID: 1, KnowledgeBaseID: f.ds.KnowledgeBaseID, Type: "paperless", SyncMode: types.SyncModeManual, Status: types.DataSourceStatusActive, Config: blob})
	require.NoError(t, err)
	require.Equal(t, types.DataSourceStatusPaused, ds.Status)
	require.False(t, ds.SyncDeletions)
	require.Equal(t, 1, f.scheduler.EntryCount(), "only pre-existing sync remains scheduled")
	parsed, err := ds.ParseConfig()
	require.NoError(t, err)
	require.Empty(t, parsed.ResourceIDs)
	_, err = svc.ManualSync(context.Background(), ds.ID)
	require.ErrorContains(t, err, "do not support sync")
	require.ErrorContains(t, svc.ResumeDataSource(context.Background(), ds.ID), "do not support sync")
	updated := *ds
	updated.SyncMode = types.SyncModeIncremental
	_, err = svc.UpdateDataSource(context.Background(), &updated)
	require.ErrorContains(t, err, "cannot be converted")
	log := &types.SyncLog{ID: "manual-stale-task", DataSourceID: ds.ID, TenantID: 1, Status: types.SyncLogStatusRunning}
	require.NoError(t, f.syncLogRepo.Create(context.Background(), log))
	payload, _ := json.Marshal(types.DataSourceSyncPayload{DataSourceID: ds.ID, SyncLogID: log.ID, TenantID: 1})
	require.NoError(t, svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload)))
	saved, err := f.syncLogRepo.FindByID(context.Background(), log.ID)
	require.NoError(t, err)
	require.Equal(t, types.SyncLogStatusCanceled, saved.Status)
	logs, err := f.syncLogRepo.FindByDataSource(context.Background(), ds.ID, 10, 0)
	require.NoError(t, err)
	require.Len(t, logs, 1, "manual connection APIs never created a sync log")
}

func TestBytesToFileHeaderPreservesQuotedOriginalName(t *testing.T) {
	filename := `invoice "2026".txt`
	file, err := bytesToFileHeader([]byte("synthetic original"), filename)
	require.NoError(t, err)
	require.Equal(t, filename, file.Filename)
	stream, err := file.Open()
	require.NoError(t, err)
	defer stream.Close()
	content, err := io.ReadAll(stream)
	require.NoError(t, err)
	require.Equal(t, []byte("synthetic original"), content)
}
