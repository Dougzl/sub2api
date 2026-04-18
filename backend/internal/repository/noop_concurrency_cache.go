package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type noopConcurrencyCache struct{}

func (noopConcurrencyCache) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	return true, nil
}
func (noopConcurrencyCache) ReleaseAccountSlot(context.Context, int64, string) error { return nil }
func (noopConcurrencyCache) GetAccountConcurrency(context.Context, int64) (int, error) {
	return 0, nil
}
func (noopConcurrencyCache) GetAccountConcurrencyBatch(_ context.Context, accountIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(accountIDs))
	for _, id := range accountIDs {
		out[id] = 0
	}
	return out, nil
}
func (noopConcurrencyCache) IncrementAccountWaitCount(context.Context, int64, int) (bool, error) {
	return true, nil
}
func (noopConcurrencyCache) DecrementAccountWaitCount(context.Context, int64) error { return nil }
func (noopConcurrencyCache) GetAccountWaitingCount(context.Context, int64) (int, error) {
	return 0, nil
}
func (noopConcurrencyCache) AcquireUserSlot(context.Context, int64, int, string) (bool, error) {
	return true, nil
}
func (noopConcurrencyCache) ReleaseUserSlot(context.Context, int64, string) error { return nil }
func (noopConcurrencyCache) GetUserConcurrency(context.Context, int64) (int, error) {
	return 0, nil
}
func (noopConcurrencyCache) IncrementWaitCount(context.Context, int64, int) (bool, error) {
	return true, nil
}
func (noopConcurrencyCache) DecrementWaitCount(context.Context, int64) error { return nil }
func (noopConcurrencyCache) GetAccountsLoadBatch(_ context.Context, accounts []service.AccountWithConcurrency) (map[int64]*service.AccountLoadInfo, error) {
	out := make(map[int64]*service.AccountLoadInfo, len(accounts))
	for _, account := range accounts {
		out[account.ID] = &service.AccountLoadInfo{AccountID: account.ID}
	}
	return out, nil
}
func (noopConcurrencyCache) GetUsersLoadBatch(_ context.Context, users []service.UserWithConcurrency) (map[int64]*service.UserLoadInfo, error) {
	out := make(map[int64]*service.UserLoadInfo, len(users))
	for _, user := range users {
		out[user.ID] = &service.UserLoadInfo{UserID: user.ID}
	}
	return out, nil
}
func (noopConcurrencyCache) CleanupExpiredAccountSlots(context.Context, int64) error { return nil }
func (noopConcurrencyCache) CleanupStaleProcessSlots(context.Context, string) error  { return nil }
