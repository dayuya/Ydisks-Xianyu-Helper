package adapter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	orderapp "xianyu-go/internal/application/orders"
	"xianyu-go/internal/xianyu/mtop"
)

// TestOrderRuntimeStandardSellerCursorAndHistory 验证实际适配器与 MTOP 客户端按平台游标分页，并把保留历史语义传给应用层。
func TestOrderRuntimeStandardSellerCursorAndHistory(t *testing.T) {
	// merchantCalls、standardCalls 由顺序请求回调统计；本测试不启动并发工作器。
	merchantCalls, standardCalls := 0, 0
	// server 模拟一次商家拒绝及两页普通卖家记录，每页游标与条数故意不一致。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/merchant" {
			merchantCalls++
			_, _ = io.WriteString(w, `{"ret":["PERMISSION_EXCEPTION::无权限访问"]}`)
			return
		}
		standardCalls++
		// parseErr 保存表单读取错误，避免测试以空参数继续运行。
		if parseErr := r.ParseForm(); parseErr != nil {
			t.Error("普通列表表单无效")
		}
		// payload 保存请求的分页事实，值不包含真实身份或会话。
		var payload map[string]any
		if json.Unmarshal([]byte(r.Form.Get("data")), &payload) != nil {
			t.Error("普通列表分页参数无效")
		}
		if standardCalls == 1 {
			if payload["pageNumber"] != float64(1) || payload["offsetRow"] != float64(0) {
				t.Error("第一页游标错误")
			}
			_, _ = io.WriteString(w, `{"ret":["SUCCESS::调用成功"],"data":{"items":[{"bizOrderId":"order-one","auctionId":"item-one","sellerId":"7","orderStatus":4,"totalFee":"12.00"}],"nextPage":true,"lastEndRow":17}}`)
			return
		}
		if payload["pageNumber"] != float64(2) || payload["offsetRow"] != float64(17) {
			t.Error("第二页未沿用平台游标")
		}
		_, _ = io.WriteString(w, `{"ret":["SUCCESS::调用成功"],"data":{"items":[{"bizOrderId":"order-two","auctionId":"item-two","sellerId":"7","orderStatus":3,"totalFee":"8.90"}],"nextPage":false}}`)
	}))
	defer server.Close()
	// client 的两个端点都固定为本地替身。
	client := &mtop.ClientImpl{HTTPClient: server.Client(), SoldOrdersURL: server.URL + "/merchant", StandardSoldOrdersURL: server.URL + "/standard"}
	// runtime 只装配平台查询能力，不含数据库、自动化和发货能力。
	runtime := NewOrderRuntime(nil, OrderRuntimeHooks{Client: func() mtop.Client { return client }}, nil, nil)
	// result、err 保存完整平台分页结果，只有所有页面成功才可交给应用层。
	result, err := runtime.FetchSoldOrders(context.Background(), &orderapp.PlatformRuntimeData{Value: "unb=7; _m_h5_tk=fixture_1"})
	if err != nil || merchantCalls != 1 || standardCalls != 2 || !result.PreserveMissing || result.SellerID != "7" || len(result.Orders) != 2 || result.Orders[0].OrderStatus != "completed" || result.Orders[1].OrderStatus != "shipped" {
		t.Fatalf("普通卖家分页或保留历史语义错误: merchant=%d standard=%d err=%v", merchantCalls, standardCalls, err)
	}
}
