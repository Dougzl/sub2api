package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type noopSchedulerCache struct{}

func (noopSchedulerCache) GetSnapshot(context.Context, service.SchedulerBucket) ([]*service.Account, bool, error) {
	return nil, false, nil
}
func (noopSchedulerCache) SetSnapshot(context.Context, service.SchedulerBucket, []service.Account) error {
	return nil
}
func (noopSchedulerCache) GetAccount(context.Context, int64) (*service.Account, error) {
	return nil, nil
}
func (noopSchedulerCache) SetAccount(context.Context, *service.Account) error        { return nil }
func (noopSchedulerCache) DeleteAccount(context.Context, int64) error                { return nil }
func (noopSchedulerCache) UpdateLastUsed(context.Context, map[int64]time.Time) error { return nil }
func (noopSchedulerCache) TryLockBucket(context.Context, service.SchedulerBucket, time.Duration) (bool, error) {
	return true, nil
}
func (noopSchedulerCache) ListBuckets(context.Context) ([]service.SchedulerBucket, error) {
	return nil, nil
}
func (noopSchedulerCache) GetOutboxWatermark(context.Context) (int64, error) { return 0, nil }
func (noopSchedulerCache) SetOutboxWatermark(context.Context, int64) error   { return nil }
