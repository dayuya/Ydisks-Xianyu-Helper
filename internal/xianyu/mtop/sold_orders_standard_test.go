package mtop

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"xianyu-go/internal/xianyu/cookierefresh"
)

// standardSoldFixture 仅包含合成身份，金额刻意与商品单价不同以防错误取价。
const standardSoldFixture = `{"bizOrderId":"9876543210123456789","auctionId":"item-safe","sellerId":"7","buyerId":"buyer-safe","orderStatus":4,"orderStatusMsg":"买家已确认收货，交易成功","buyAmount":1,"totalFee":"129.90","auctionPrice":"8.90","createTime":1700000000000}`

// TestSoldOrdersFallbackOnlyOnExplicitPermission 验证普通接口只在明确权限码后调用，保留其他失败语义。
func TestSoldOrdersFallbackOnlyOnExplicitPermission(t *testing.T) {
	// testCase 覆盖正常商家、权限拒绝以及不允许回退的认证和网络状态。
	for _, testCase := range []struct {
		// name 标识当前平台响应场景。
		name string
		// ret 是商家端点的合成状态码。
		ret string
		// status 是商家端点返回的 HTTP 状态。
		status int
		// fallback 表示应当出现一次普通卖家请求。
		fallback bool
	}{
		{name: "permission", ret: "PERMISSION_EXCEPTION::无权限访问", status: 200, fallback: true},
		{name: "merchant success", ret: "SUCCESS::调用成功", status: 200},
		{name: "session", ret: "FAIL_SYS_SESSION_EXPIRED::失效", status: 200},
		{name: "risk", ret: "FAIL_SYS_USER_VALIDATE::需要验证", status: 200},
		{name: "http", ret: "PERMISSION_EXCEPTION::无权限访问", status: 403},
		{name: "different code", ret: "FAIL::PERMISSION_EXCEPTION", status: 200},
	} {
		// t 承载当前场景断言，每个场景独占 HTTP 服务和请求计数。
		t.Run(testCase.name, func(t *testing.T) {
			// calls 统计普通卖家请求，只允许权限场景出现一次。
			calls := 0
			// server 根据请求路径返回两种真实协议形状，所有请求都停留在本地。
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/standard" {
					calls++
					if r.URL.Query().Get("api") != "mtop.taobao.idle.trade.sold.get" || r.URL.Query().Get("v") != "5.0" || r.URL.Query().Get("type") != "originaljson" || r.Header.Get("Origin") != "https://h5.m.goofish.com" || r.Header.Get("idle_site_biz_code") != "" {
						t.Error("普通卖家请求协议或来源不正确")
					}
					// err 保存本地请求表单解析错误，不包含真实会话。
					if err := r.ParseForm(); err != nil {
						t.Error("请求表单不可解析")
					}
					// payload 保存合成请求数据，只验证分页和所有状态查询。
					var payload map[string]any
					if json.Unmarshal([]byte(r.Form.Get("data")), &payload) != nil || payload["orderStatus"] != "ALL" || payload["offsetRow"] != float64(0) || payload["pageNumber"] != float64(1) {
						t.Error("普通卖家请求未覆盖全部状态或分页错误")
					}
					if !strings.Contains(r.Header.Get("Cookie"), "sid=rotated") {
						t.Error("回退没有使用商家响应已经更新的会话")
					}
					_, _ = io.WriteString(w, `{"ret":["SUCCESS::调用成功"],"data":{"items":[`+standardSoldFixture+`],"nextPage":false}}`)
					return
				}
				http.SetCookie(w, &http.Cookie{Name: "sid", Value: "rotated", Path: "/"})
				w.WriteHeader(testCase.status)
				_ = json.NewEncoder(w).Encode(map[string]any{"ret": []string{testCase.ret}, "data": map[string]any{"module": map[string]any{"items": []any{}, "nextPage": false}}})
			}))
			defer server.Close()
			// client 的两个端点均为本地替身，禁止失败测试意外访问真实平台。
			client := &ClientImpl{HTTPClient: server.Client(), SoldOrdersURL: server.URL + "/merchant", StandardSoldOrdersURL: server.URL + "/standard"}
			// page、err 保存自动回退后的结果，真实 Cookie 不参与测试。
			page, err := client.FetchSoldOrdersPage(context.Background(), "unb=7; _m_h5_tk=fixture_1", 1, 30)
			if testCase.fallback {
				if err != nil || calls != 1 || page == nil || !page.PreserveMissing || len(page.Items) != 1 || page.Items[0].OrderStatus != "completed" || page.Items[0].Amount != "129.90" || page.Items[0].OrderID != "9876543210123456789" {
					t.Fatalf("普通卖家回退、金额或历史状态错误: calls=%d err=%v", calls, err)
				}
			} else if calls != 0 || (testCase.name == "merchant success" && (err != nil || page.PreserveMissing)) || (testCase.name != "merchant success" && err == nil) {
				t.Fatal("非权限场景改变了原有成功或失败语义")
			}
		})
	}
}

// TestStandardSoldOrdersCompleteness 验证普通卖家响应的归属、完整性与游标，异常响应绝不能变成空列表。
func TestStandardSoldOrdersCompleteness(t *testing.T) {
	// testCase 是当前平台边界；ok 表示应成功解析，next 表示应继续分页。
	for _, testCase := range []struct {
		// name 是当前响应边界名称。
		name string
		// data 是合成的 data 内容，不包含真实账号信息。
		data string
		// ok 与 next 分别声明解析成功及下一页语义。
		ok, next bool
	}{
		{name: "empty", data: `{"items":[],"nextPage":false}`, ok: true},
		{name: "null empty", data: `{"items":null,"nextPage":"false"}`, ok: true},
		{name: "missing list", data: `{"nextPage":false}`},
		{name: "invalid list", data: `{"items":{},"nextPage":false}`},
		{name: "missing next", data: `{"items":[]}`},
		{name: "invalid next", data: `{"items":[],"nextPage":2}`},
		{name: "empty next", data: `{"items":[],"nextPage":true,"lastEndRow":1}`},
		{name: "cursor", data: `{"items":[` + standardSoldFixture + `],"nextPage":1,"lastEndRow":17}`, ok: true, next: true},
		{name: "missing cursor", data: `{"items":[` + standardSoldFixture + `],"nextPage":true}`},
		{name: "stale cursor", data: `{"items":[` + standardSoldFixture + `],"nextPage":true,"lastEndRow":0}`},
		{name: "wrong seller", data: `{"items":[` + strings.Replace(standardSoldFixture, `"sellerId":"7"`, `"sellerId":"8"`, 1) + `],"nextPage":false}`},
		{name: "missing item", data: `{"items":[` + strings.Replace(standardSoldFixture, `"auctionId":"item-safe",`, ``, 1) + `],"nextPage":false}`},
		{name: "missing order", data: `{"items":[` + strings.Replace(standardSoldFixture, `"bizOrderId":"9876543210123456789",`, ``, 1) + `],"nextPage":false}`},
		{name: "non object", data: `{"items":["private-marker"],"nextPage":false}`},
		{name: "numeric long id and default quantity", data: `{"items":[` + strings.Replace(strings.Replace(standardSoldFixture, `"9876543210123456789"`, `9876543210123456789`, 1), `"buyAmount":1,`, ``, 1) + `],"nextPage":false}`, ok: true},
	} {
		// t 检查当前合成响应是否满足完整页契约。
		t.Run(testCase.name, func(t *testing.T) {
			// page、err 仅解析合成响应，不发起网络请求。
			page, err := (&ClientImpl{}).parseStandardSoldOrders([]byte(`{"ret":["SUCCESS::调用成功"],"data":`+testCase.data+`}`), 200, "7", 0)
			if testCase.ok {
				if err != nil || page == nil || !page.PreserveMissing || page.NextPage != testCase.next || (testCase.next && page.NextOffset != 17) {
					t.Fatalf("合法普通卖家页解析失败: %v", err)
				}
				if len(page.Items) > 0 && (page.Items[0].OrderID != "9876543210123456789" || page.Items[0].Quantity != "1") {
					t.Fatal("长订单号精度或默认数量错误")
				}
			} else if err == nil || page != nil || strings.Contains(err.Error(), "private-marker") {
				t.Fatal("非法响应未拒绝或泄露响应内容")
			}
		})
	}
}

// TestStandardSoldOrdersRequestFailures 验证普通端点参数、签名、传输、取消和响应读体失败均有受控结果。
func TestStandardSoldOrdersRequestFailures(t *testing.T) {
	// scenario 表示当前独立的请求失败边界。
	for _, scenario := range []string{"page", "offset", "address", "missing token", "identity", "scoped mismatch", "transport", "cancel", "body", "http"} {
		// t 记录当前失败场景，不调用真实网络。
		t.Run(scenario, func(t *testing.T) {
			// ctx、cancel 控制一次本地替身调用的取消生命周期。
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			// client 的传输函数只注入固定错误或合成响应，失败原因不能泄露到错误文本。
			client := &ClientImpl{HTTPClient: &http.Client{Transport: cookieSessionRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
				if scenario == "body" {
					// reader、writer 形成确定性的读体错误，不引入外部 I/O。
					reader, writer := io.Pipe()
					_ = writer.CloseWithError(errors.New("private-marker"))
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: reader}, nil
				}
				if scenario == "http" {
					return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"ret":["SUCCESS::调用成功"],"data":{"items":[],"nextPage":false}}`))}, nil
				}
				return nil, errors.New("private-marker")
			})}}
			// cookies、pageNumber、offsetRow 是触发边界用的合成参数，不包含真实登录信息。
			cookies, pageNumber, offsetRow := "unb=7; _m_h5_tk=fixture_1", 1, int64(0)
			switch scenario {
			case "page":
				pageNumber = 0
			case "offset":
				offsetRow = -1
			case "address":
				client.StandardSoldOrdersURL = "://private-marker"
			case "missing token":
				cookies = "unb=7"
			case "identity":
				cookies = "sid=fixture"
			case "scoped mismatch":
				cookies = "unb=8; _m_h5_tk=fixture_1"
				ctx, _ = WithCookieSnapshot(ctx, []cookierefresh.BrowserCookie{{Name: "unb", Value: "7", Domain: ".goofish.com", Path: "/", Secure: true}, {Name: "_m_h5_tk", Value: "fixture_1", Domain: ".goofish.com", Path: "/", Secure: true}})
			case "cancel":
				cancel()
			}
			// page、err 保存失败结果，不能返回可供落库的页面或泄露底层错误内容。
			page, err := client.fetchStandardSoldOrdersPageOnce(ctx, cookies, pageNumber, offsetRow)
			if page != nil || err == nil || strings.Contains(err.Error(), "private-marker") || (scenario == "cancel" && !errors.Is(err, context.Canceled)) || (scenario == "missing token" && !IsMTopTokenExpiredErr(err)) {
				t.Fatal("普通卖家请求失败边界未被正确保留")
			}
		})
	}
}

// TestStandardSoldOrderStatuses 验证普通接口阶段独立映射，并保留退款、关闭和未知状态。
func TestStandardSoldOrderStatuses(t *testing.T) {
	// testCase 保存平台状态数字、说明与预期内部状态。
	for _, testCase := range []struct{ status, message, want string }{
		{"1", "", "processing"}, {"2", "", "pending_ship"}, {"3", "", "shipped"}, {"4", "", "completed"},
		{"4", "退款中", "refunding"}, {"0", "交易关闭", "cancelled"}, {"0", "退款成功", "cancelled"}, {"99", "", "unknown"},
		{"2", "我已小刀，待刀成", "processing"}, {"2", "等待成团", "processing"},
	} {
		if standardSoldOrderStatus(map[string]any{"orderStatus": testCase.status, "orderStatusMsg": testCase.message}) != testCase.want {
			t.Errorf("普通接口状态映射错误: %s", testCase.status)
		}
	}
}

// TestStandardSoldOrdersRejectsInvalidEnvelope 验证错误信封、JSON 类型错误与尾随正文均不能作为成功订单页。
func TestStandardSoldOrdersRejectsInvalidEnvelope(t *testing.T) {
	// body 是当前非法平台响应，所有内容均为合成数据。
	for _, body := range []string{
		`{"ret":["SUCCESS::调用成功"],"data":{"items":[],"nextPage":false}} trailing`,
		`{"ret":["PERMISSION_EXCEPTION::无权限访问"]}`,
		`{"ret":"private-marker","data":{}}`,
	} {
		// page、err 保存非法响应解析结果，错误不得回显完整正文。
		page, err := (&ClientImpl{}).parseStandardSoldOrders([]byte(body), 200, "7", 0)
		if page != nil || err == nil || strings.Contains(err.Error(), "private-marker") {
			t.Fatal("非法普通卖家信封被当作成功")
		}
	}
}

// TestStandardSoldOrderIdentity 验证缺失身份和重复冲突会话失败，重复一致身份可以兼容。
func TestStandardSoldOrderIdentity(t *testing.T) {
	// header 是当前合成会话，valid 声明是否具有唯一合法身份。
	for header, valid := range map[string]bool{"unb=7": true, "unb=7; unb=7": true, "unb=7; unb=8": false, "sid=fake": false, "unb=": false} {
		// identity、err 保存仅在内存中使用的测试身份和错误。
		identity, err := standardSoldOrderSellerID(header)
		if valid != (err == nil) || (valid && identity != "7") {
			t.Fatal("未按唯一非空身份约束验证会话")
		}
	}
}
