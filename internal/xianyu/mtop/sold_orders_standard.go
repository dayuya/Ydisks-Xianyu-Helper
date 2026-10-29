package mtop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"xianyu-go/internal/xianyu/protocol"
)

// standardSoldOrdersAPI 是普通卖家已售列表端点，包含平台允许当前账号查看的全部状态。
const standardSoldOrdersAPI = "https://h5api.m.goofish.com/h5/mtop.taobao.idle.trade.sold.get/5.0/"

// standardSoldOrdersReferer 是普通卖家请求的官方页面来源，不复用鱼小铺工作台来源。
const standardSoldOrdersReferer = "https://h5.m.goofish.com/"

// soldOrdersPermissionDenied 只识别 HTTP 成功响应中的明确权限码；Session、风控和传输错误不得回退。
func soldOrdersPermissionDenied(err error) bool {
	// response 保存结构化平台失败，禁止根据错误正文中偶然出现的文案决定接口切换。
	var response *MTopResponseError
	if !errors.As(err, &response) || response.HTTPStatus < 200 || response.HTTPStatus >= 300 || response.Kind != MTopErrorSystem {
		return false
	}
	// ret 是本次响应中的平台错误标记，仅比较分隔符之前的完整错误码。
	for _, ret := range response.Ret {
		// code 是精确的平台错误码，后续中文错误原因不参与判断。
		code, _, _ := strings.Cut(ret, "::")
		if strings.TrimSpace(code) == "PERMISSION_EXCEPTION" {
			return true
		}
	}
	return false
}

// fetchStandardSoldOrdersPageOnce 读取 pageNumber 与 offsetRow 对应的普通卖家页，ctx 管理请求取消。
// cookies 仅用于平台请求；只返回完整解析且归属一致的页面，不把局部列表作为历史删除依据。
func (c *ClientImpl) fetchStandardSoldOrdersPageOnce(ctx context.Context, cookies string, pageNumber int, offsetRow int64) (*SoldOrdersPage, error) {
	if pageNumber < 1 || offsetRow < 0 {
		return nil, errors.New("普通卖家订单分页参数无效")
	}
	// endpoint 默认使用固定官方端点，测试可以注入本地替身。
	endpoint := c.StandardSoldOrdersURL
	if endpoint == "" {
		endpoint = standardSoldOrdersAPI
	}
	// signingCookies 与 requestCookies 分别服务于签名和实际端点，明文禁止进入日志。
	signingCookies, requestCookies := mtopRequestCookies(ctx, cookies, standardSoldOrdersReferer, endpoint)
	// sellerID、identityErr 保存实际请求中唯一的卖家身份；不从响应买家字段推断归属。
	sellerID, identityErr := standardSoldOrderSellerID(requestCookies)
	if identityErr != nil {
		return nil, identityErr
	}
	// storedSellerID、storedIdentityErr 核对扁平视图和真实端点的身份一致，拒绝路径作用域造成的账号混用。
	storedSellerID, storedIdentityErr := standardSoldOrderSellerID(cookies)
	if storedIdentityErr != nil || storedSellerID != sellerID {
		return nil, errors.New("普通卖家订单请求会话与账号身份不一致")
	}
	// token 是当前签名所需凭据，仅在本次请求内使用。
	token := protocol.SignToken(signingCookies)
	if token == "" {
		return nil, &MTopResponseError{Kind: MTopErrorTokenExpired, API: "普通卖家订单接口", Detail: "cookie 缺少 _m_h5_tk"}
	}
	// payload 固定请求所有状态，不能以待发货子集替代用户请求的历史订单列表。
	payload, _ := json.Marshal(map[string]any{"pageNumber": pageNumber, "orderStatus": "ALL", "offsetRow": offsetRow})
	// timestamp 和 query 保存请求签名时间及参数，含签名的 URL 禁止输出。
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	// query 保存普通卖家5.0版本所需参数；签名和完整 URL 禁止记录。
	query := url.Values{
		"jsv": {"2.7.2"}, "appKey": {protocol.SignAppKey}, "t": {timestamp},
		"sign": {protocol.GenerateSign(timestamp, token, string(payload))}, "v": {"5.0"},
		"type": {"originaljson"}, "accountSite": {"xianyu"}, "dataType": {"json"},
		"timeout": {"20000"}, "api": {"mtop.taobao.idle.trade.sold.get"},
		"valueType": {"original"}, "sessionOption": {"AutoLoginOnly"},
	}
	// request、requestErr 保存带取消上下文的请求；错误只返回固定文本避免暴露签名。
	request, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"?"+query.Encode(), strings.NewReader(url.Values{"data": {string(payload)}}.Encode()))
	if requestErr != nil {
		return nil, errors.New("普通卖家订单请求地址无效")
	}
	setCommonHeaders(request, requestCookies)
	request.Header.Set("Origin", "https://h5.m.goofish.com")
	request.Header.Set("Referer", standardSoldOrdersReferer)
	// response、callErr 保存平台响应，超时和取消保留既有请求生命周期。
	response, callErr := c.httpClient().Do(request)
	if callErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("普通卖家订单请求失败")
	}
	defer response.Body.Close()
	absorbMTopResponseCookies(ctx, cookies, response)
	// raw、readErr 受既有响应大小限制，正文只在当前函数作用域用于解析。
	raw, readErr := readMTopBody(response)
	if readErr != nil {
		return nil, c.mtopResponseFailureWithCause("普通卖家订单接口", response.StatusCode, nil, "读取响应失败", readErr)
	}
	return c.parseStandardSoldOrders(raw, response.StatusCode, sellerID, offsetRow)
}

// standardSoldOrderSellerID 从 header 的 unb 读取唯一非空身份；重复冲突或缺失时返回不含凭据的错误。
func standardSoldOrderSellerID(header string) (string, error) {
	// sellerID 保存当前已确认身份，found 区分未出现和空值。
	sellerID, found := "", false
	// part 是当前 Cookie 片段，仅在当前作用域内比较。
	for _, part := range strings.Split(header, ";") {
		// name、value、ok 保存键值拆分，仅关注平台身份字段。
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || name != "unb" {
			continue
		}
		if found && sellerID != value {
			return "", errors.New("普通卖家订单请求身份冲突")
		}
		sellerID, found = value, true
	}
	if strings.TrimSpace(sellerID) == "" {
		return "", errors.New("普通卖家订单请求缺少账号身份")
	}
	return sellerID, nil
}

// parseStandardSoldOrders 校验平台信封、逐单归属和分页，raw 不得进入错误或日志；offsetRow 用于拒绝不前进的游标。
func (c *ClientImpl) parseStandardSoldOrders(raw []byte, status int, sellerID string, offsetRow int64) (*SoldOrdersPage, error) {
	if !json.Valid(raw) {
		return nil, c.mtopResponseFailure("普通卖家订单接口", status, nil, "JSON 解析失败")
	}
	// decoded 保留平台原始数字，避免长订单号与金额经浮点转换后失真。
	var decoded struct {
		// Ret 保存平台状态，只通过统一的脱敏错误分类传播。
		Ret []string `json:"ret"`
		// Data 是普通卖家页，缺失必要字段不代表合法空列表。
		Data map[string]any `json:"data"`
	}
	// decoder 保持数字文本精度，禁止把响应正文输出到诊断。
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	// decodeErr 保存解析失败原因，只通过统一错误分类输出固定说明。
	if decodeErr := decoder.Decode(&decoded); decodeErr != nil {
		return nil, c.mtopResponseFailureWithCause("普通卖家订单接口", status, nil, "JSON 解析失败", decodeErr)
	}
	if status < 200 || status >= 300 || !hasMTopSuccess(decoded.Ret) {
		return nil, c.mtopResponseFailure("普通卖家订单接口", status, decoded.Ret, "平台未返回成功")
	}
	// rawItems 与 present 区分显式空列表和不完整响应。
	rawItems, present := decoded.Data["items"]
	// items、valid 保存普通卖家顶层列表，兼容显式 null 空页。
	items, valid := rawItems.([]any)
	if !present || (rawItems != nil && !valid) {
		return nil, errors.New("普通卖家订单响应 items 缺失或类型非法")
	}
	// flag 保存待校验的分页标记；JSON 数字仅允许明确的0与1。
	flag := decoded.Data["nextPage"]
	// number、ok 表示分页值是否为需要保持精度的 JSON 数字。
	if number, ok := flag.(json.Number); ok {
		flag = number.String()
	}
	// next、nextValid 使用原有布尔兼容语义，未知形状不能冒充最后一页。
	next, nextValid := soldOrdersNextPage(flag)
	if !nextValid || (next && len(items) == 0) {
		return nil, errors.New("普通卖家订单分页不完整")
	}
	// page 始终要求保留本地未返回记录，平台可见时间窗口不能证明旧订单不存在。
	page := &SoldOrdersPage{PreserveMissing: true, NextPage: next, TotalCount: mtopInt(decoded.Data["totalCount"])}
	if next {
		// cursor、cursorErr 读取下一页原始游标，禁止按页大小猜测位置。
		cursor, cursorErr := strconv.ParseInt(mtopString(decoded.Data["lastEndRow"]), 10, 64)
		if cursorErr != nil || cursor <= offsetRow {
			return nil, errors.New("普通卖家订单下一页游标缺失或未前进")
		}
		page.NextOffset = cursor
	}
	// rawItem 是当前待解析记录；任何身份或结构错误都拒绝整页，避免静默丢单。
	for _, rawItem := range items {
		// item、ok 保存平台记录的对象结构。
		item, ok := rawItem.(map[string]any)
		if !ok || mtopString(item["sellerId"]) != sellerID || strings.TrimSpace(mtopString(item["bizOrderId"])) == "" || strings.TrimSpace(mtopString(item["auctionId"])) == "" {
			return nil, errors.New("普通卖家订单条目缺少必要身份或卖家归属不一致")
		}
		// quantity 保存购买数量，缺失时沿用既有列表默认值。
		quantity := firstMTopString(item, "buyAmount")
		if quantity == "" || quantity == "0" {
			quantity = "1"
		}
		// statusMessage 只用于保留平台明确的小刀或成团事实，不能从是否改价推断砍价订单。
		statusMessage := firstMTopString(item, "orderStatusMsg", "statusMsg")
		page.Items = append(page.Items, SoldOrder{
			OrderID: mtopString(item["bizOrderId"]), ItemID: mtopString(item["auctionId"]),
			BuyerID: mtopString(item["buyerId"]), CreatedAt: soldOrderCreatedAt(item, nil),
			OrderStatus: standardSoldOrderStatus(item), Quantity: quantity,
			Amount:    firstMTopString(item, "totalFee", "amount"),
			IsBargain: strings.Contains(statusMessage, "小刀") || strings.Contains(statusMessage, "刀成") || strings.Contains(statusMessage, "成团"),
		})
	}
	return page, nil
}

// standardSoldOrderStatus 识别普通接口状态与说明；未知状态保持 unknown，不能猜测为待发货。
func standardSoldOrderStatus(item map[string]any) string {
	// message 保存平台实际状态说明，退款和关闭文案优先于数字状态。
	message := firstMTopString(item, "orderStatusMsg", "statusMsg")
	if strings.Contains(message, "待刀成") || strings.Contains(message, "待成团") {
		return "processing"
	}
	if strings.Contains(message, "退款中") {
		return "refunding"
	}
	if strings.Contains(message, "交易关闭") || strings.Contains(message, "退款成功") {
		return "cancelled"
	}
	// status 保存普通接口明确的订单阶段；商家接口的数字映射不可直接复用。
	status := firstMTopString(item, "orderStatus", "status")
	switch status {
	case "1":
		return "processing"
	case "2":
		return "pending_ship"
	case "3":
		return "shipped"
	case "4":
		return "completed"
	}
	return normalizeSoldOrderStatus(message, false)
}
