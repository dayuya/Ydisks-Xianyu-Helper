package orders

import (
	"context"
	"errors"
	"testing"
)

// TestRefreshPartialSoldOrdersPreservesHistory 验证普通卖家可见列表只合并返回记录，不清理未返回的历史订单。
func TestRefreshPartialSoldOrdersPreservesHistory(t *testing.T) {
	// repository 将删除路径设为失败，确保测试证明没有调用删除而非删除数量恰好为零。
	repository := &refreshRepositoryFake{
		owned: map[string]bool{"cookie-1": true}, detail: &PlatformRuntimeData{UserID: 7, Value: "fixture"},
		orders:    map[string]*Order{"historical": {OrderID: "historical", CookieID: "cookie-1", OrderStatus: "completed", Amount: "9.00"}},
		deleteErr: errors.New("有限范围列表不得清理历史订单"),
	}
	// runtime 返回普通卖家可见的一笔已完成订单，不要求已有订单也出现在本页。
	runtime := &refreshRuntimeFake{soldAvailable: true, soldResult: RefreshSoldFetchResult{PreserveMissing: true, Orders: []RefreshSoldOrder{{OrderID: "new-order", OrderStatus: "completed", Amount: "129.90", ItemID: "item-safe"}}}}
	// result、err 保存完整应用用例的写入结果；调用失败意味着错误触发了删除或其他持久化路径。
	result, err := NewRefreshService(repository, runtime, 1).Refresh(context.Background(), 7, "cookie-1", "all")
	if err != nil || result.Summary.Failed != 0 || result.Summary.Discovered != 1 || result.Summary.SoftDeleted != 0 || len(repository.orders) != 2 || repository.orders["historical"].OrderStatus != "completed" || repository.orders["new-order"].Amount != "129.90" {
		t.Fatalf("普通卖家列表未安全合并历史订单: err=%v summary=%+v", err, result.Summary)
	}
}

// TestRefreshEmptyPartialSoldOrdersDoesNotDelete 验证普通卖家合法空页也不能删除本地旧订单。
func TestRefreshEmptyPartialSoldOrdersDoesNotDelete(t *testing.T) {
	// repository 的删除错误哨兵确保空列表处理确实跳过清理调用。
	repository := &refreshRepositoryFake{owned: map[string]bool{"cookie-1": true}, detail: &PlatformRuntimeData{UserID: 7, Value: "fixture"}, deleteErr: errors.New("不得删除")}
	// runtime 只返回受限范围标记和合法空列表。
	runtime := &refreshRuntimeFake{soldAvailable: true, soldResult: RefreshSoldFetchResult{PreserveMissing: true}}
	// result、err 保存空结果同步状态，空成功不得转成整账号清理。
	result, err := NewRefreshService(repository, runtime, 1).Refresh(context.Background(), 7, "cookie-1", "all")
	if err != nil || result.Summary.Failed != 0 || result.Summary.SoftDeleted != 0 {
		t.Fatal("普通卖家空页错误清理了历史记录")
	}
}
