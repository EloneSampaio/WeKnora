package datasource

import (
	"context"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSchedulerNeverRunsManualConnection(t *testing.T) {
	repo := newFakeDataSourceRepo()
	queue := &fakeTaskEnqueuer{}
	scheduler := NewScheduler(repo, newFakeSyncLogRepo(), queue)
	// Also protect against legacy/corrupt stored active+schedulable state.
	ds := &types.DataSource{ID: "manual", TenantID: 1, Type: "paperless", SyncMode: types.SyncModeManual, Status: types.DataSourceStatusActive, SyncSchedule: "0 0 * * * *"}
	require.NoError(t, repo.Create(context.Background(), ds))
	require.NoError(t, scheduler.Start(context.Background()))
	defer scheduler.Stop()
	require.Zero(t, scheduler.EntryCount())
	require.NoError(t, scheduler.AddOrUpdate(ds))
	require.Zero(t, scheduler.EntryCount())
	scheduler.triggerSync(ds.ID, ds.TenantID)
	require.Zero(t, queue.count.Load())
}
