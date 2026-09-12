package handler_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/lp/campus-market/internal/config"
	"github.com/lp/campus-market/internal/constants"
	"github.com/lp/campus-market/internal/model"
	"github.com/lp/campus-market/internal/router"
	"github.com/lp/campus-market/internal/util"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// ----- test harness ---------------------------------------------------------

// apiFixture builds the REAL Gin engine (routes, JWT auth middleware, error
// handler, rate limiter and DTO visibility) backed by an isolated, file-based
// SQLite database, so assertions exercise the full HTTP entry point rather
// than the service layer directly. Each test is hermetic and re-runnable.
type apiFixture struct {
	t       *testing.T
	db      *gorm.DB
	server  *httptest.Server
	buyerID uint
	sellerID uint
	otherID uint
	tokens  map[uint]string
}

// envelope is the unified {code,message,data} API response.
type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// orderDTO mirrors the role-aware order view (only the fields assertions use).
type orderDTO struct {
	ID                uint       `json:"id"`
	ProductID         uint       `json:"product_id"`
	BuyerID           uint       `json:"buyer_id"`
	SellerID          uint       `json:"seller_id"`
	Status            string     `json:"status"`
	HandoverCode      string     `json:"handover_code"`
	HandoverExpiresAt *time.Time `json:"handover_expires_at"`
	HandoverUsedAt    *time.Time `json:"handover_used_at"`
	HandoverStatus    string     `json:"handover_status"`
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "api_handover_test.db") + "?_pragma=busy_timeout(10000)&_pragma=foreign_keys(off)"
	gdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// The test router.New runs AutoMigrate, but also migrate here to seed
	// users/products/orders directly through GORM.
	if err := gdb.AutoMigrate(
		&model.User{}, &model.Product{}, &model.Conversation{}, &model.Message{},
		&model.TradeOrder{}, &model.Review{}, &model.BookExchange{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	cfg := &config.Config{
		Port:               "8080",
		JWTSecret:          "test-secret",
		JWTExpireHours:     72,
		RateLimitPerMin:    10000,
		LoginRateLimit:     10000,
		CORSOrigins:        []string{"*"},
		SeedingEnabled:     false,
		HandoverTTLMinutes: 30,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := router.New(cfg, gdb, logger)
	server := httptest.NewServer(engine)

	fx := &apiFixture{
		t:      t,
		db:     gdb,
		server: server,
		tokens: map[uint]string{},
	}

	// Seed three students; tokens are minted through the SAME util the auth
	// middleware verifies, exercising the real JWT path.
	fx.buyerID = fx.seedUser("13100000001", "买家")
	fx.sellerID = fx.seedUser("13100000002", "卖家")
	fx.otherID = fx.seedUser("13100000003", "路人")
	return fx
}

func (f *apiFixture) seedUser(phone, nickname string) uint {
	u := &model.User{Phone: phone, Nickname: nickname, Role: constants.UserRoleStudent}
	if err := f.db.Create(u).Error; err != nil {
		f.t.Fatalf("seed user %s: %v", phone, err)
	}
	token, err := util.GenerateToken("test-secret", u.ID, u.Phone, u.Role, 72)
	if err != nil {
		f.t.Fatalf("mint token: %v", err)
	}
	f.tokens[u.ID] = token
	return u.ID
}

func (f *apiFixture) createProduct(sellerID uint, title string) *model.Product {
	p := &model.Product{
		SellerID: sellerID, Title: title, Description: "api test", Price: 9.9,
		Category: constants.ProductCategoryBooks, Condition: "全新",
		Campus: "东校区", TradeLocation: "东门", Status: constants.ProductStatusOnSale,
	}
	if err := f.db.Create(p).Error; err != nil {
		f.t.Fatalf("seed product: %v", err)
	}
	return p
}

// request issues an HTTP call with optional JWT and JSON body.
func (f *apiFixture) request(method, path string, userID uint, body any) (int, envelope) {
	f.t.Helper()
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			f.t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, f.server.URL+path, reader)
	if err != nil {
		f.t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if userID != 0 {
		req.Header.Set("Authorization", "Bearer "+f.tokens[userID])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var env envelope
	_ = json.Unmarshal(raw, &env)
	return resp.StatusCode, env
}

func decodeOrder(t *testing.T, env envelope) orderDTO {
	t.Helper()
	var o orderDTO
	if err := json.Unmarshal(env.Data, &o); err != nil {
		t.Fatalf("decode order: %v raw=%s", err, string(env.Data))
	}
	return o
}

// placeOrder creates an order through the HTTP API.
func (f *apiFixture) placeOrder(buyerID, productID uint) orderDTO {
	f.t.Helper()
	status, env := f.request(http.MethodPost, "/api/v1/trade-orders", buyerID, map[string]any{"product_id": productID})
	if status != http.StatusOK || env.Code != constants.CodeOK {
		f.t.Fatalf("place order http=%d code=%d msg=%s", status, env.Code, env.Message)
	}
	return decodeOrder(f.t, env)
}

func (f *apiFixture) buyerConfirm(userID, orderID uint) (httpStatus int, env envelope) {
	return f.request(http.MethodPost, "/api/v1/trade-orders/"+itoa(orderID)+"/buyer-confirm", userID, nil)
}

func (f *apiFixture) verify(userID, orderID uint, code string) (httpStatus int, env envelope) {
	return f.request(http.MethodPost, "/api/v1/trade-orders/"+itoa(orderID)+"/handover-code/verify", userID, map[string]any{"code": code})
}

func (f *apiFixture) regenerate(userID, orderID uint) (httpStatus int, env envelope) {
	return f.request(http.MethodPost, "/api/v1/trade-orders/"+itoa(orderID)+"/handover-code/regenerate", userID, nil)
}

func (f *apiFixture) listOrders(userID uint) []orderDTO {
	f.t.Helper()
	status, env := f.request(http.MethodGet, "/api/v1/trade-orders/me?page=1&page_size=100", userID, nil)
	if status != http.StatusOK || env.Code != constants.CodeOK {
		f.t.Fatalf("list orders http=%d code=%d msg=%s", status, env.Code, env.Message)
	}
	var page struct {
		Items []orderDTO `json:"items"`
	}
	if err := json.Unmarshal(env.Data, &page); err != nil {
		f.t.Fatalf("decode page: %v", err)
	}
	return page.Items
}

func (f *apiFixture) findOrder(userID, orderID uint) orderDTO {
	for _, o := range f.listOrders(userID) {
		if o.ID == orderID {
			return o
		}
	}
	f.t.Fatalf("order %d not visible to user %d", orderID, userID)
	return orderDTO{}
}

func (f *apiFixture) reloadOrderModel(orderID uint) model.TradeOrder {
	var o model.TradeOrder
	if err := f.db.First(&o, orderID).Error; err != nil {
		f.t.Fatalf("reload order: %v", err)
	}
	return o
}

func (f *apiFixture) reloadProductModel(productID uint) model.Product {
	var p model.Product
	if err := f.db.First(&p, productID).Error; err != nil {
		f.t.Fatalf("reload product: %v", err)
	}
	return p
}

func (f *apiFixture) close() { f.server.Close() }

func itoa(v uint) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

// ----- tests ----------------------------------------------------------------

// TestAPI_BuyerConfirm_GeneratesBuyerOnlyCode covers buyer confirmation through
// HTTP: a one-time code is minted, only the buyer's view contains it, and the
// route rejects anonymous, non-buyer and repeat confirmations.
func TestAPI_BuyerConfirm_GeneratesBuyerOnlyCode(t *testing.T) {
	fx := newAPIFixture(t)
	defer fx.close()

	product := fx.createProduct(fx.sellerID, "接口测试-买家确认")
	ord := fx.placeOrder(fx.buyerID, product.ID)

	// Route auth: anonymous buyer-confirm is rejected.
	status, env := fx.request(http.MethodPost, "/api/v1/trade-orders/"+itoa(ord.ID)+"/buyer-confirm", 0, nil)
	if status != http.StatusUnauthorized || env.Code != constants.CodeUnauthorized {
		t.Fatalf("anonymous confirm want 401/%d, got %d/%d", constants.CodeUnauthorized, status, env.Code)
	}

	// Permission: the seller cannot confirm the buyer's order.
	status, env = fx.buyerConfirm(fx.sellerID, ord.ID)
	if status != http.StatusForbidden || env.Code != constants.CodeForbidden {
		t.Fatalf("seller confirm want 403/%d, got %d/%d msg=%s", constants.CodeForbidden, status, env.Code, env.Message)
	}

	// Happy path: buyer confirms and gets a 6-digit, unused, time-boxed code.
	status, env = fx.buyerConfirm(fx.buyerID, ord.ID)
	if status != http.StatusOK || env.Code != constants.CodeOK {
		t.Fatalf("buyer confirm want 200/0, got %d/%d msg=%s", status, env.Code, env.Message)
	}
	confirmed := decodeOrder(t, env)
	if confirmed.Status != constants.TradeStatusConfirmed {
		t.Fatalf("status = %s, want confirmed", confirmed.Status)
	}
	if len(confirmed.HandoverCode) != 6 {
		t.Fatalf("handover code = %q, want 6 digits", confirmed.HandoverCode)
	}
	if confirmed.HandoverStatus != constants.HandoverCodeUnused || confirmed.HandoverExpiresAt == nil {
		t.Fatalf("bad handover view: %+v", confirmed)
	}

	// Visibility across the list endpoint (real DTO masking).
	buyerView := fx.findOrder(fx.buyerID, ord.ID)
	if buyerView.HandoverCode != confirmed.HandoverCode {
		t.Fatalf("buyer must see code, got %q want %q", buyerView.HandoverCode, confirmed.HandoverCode)
	}
	sellerView := fx.findOrder(fx.sellerID, ord.ID)
	if sellerView.HandoverCode != "" {
		t.Fatalf("seller must NOT see the code, got %q", sellerView.HandoverCode)
	}
	otherOrders := fx.listOrders(fx.otherID)
	for _, o := range otherOrders {
		if o.ID == ord.ID {
			t.Fatalf("third party must not see the order at all")
		}
	}

	// State rule: a second buyer confirm is a conflict.
	status, env = fx.buyerConfirm(fx.buyerID, ord.ID)
	if status != http.StatusConflict || env.Code != constants.CodeConflict {
		t.Fatalf("repeat confirm want 409/%d, got %d/%d", constants.CodeConflict, status, env.Code)
	}
}

// TestAPI_SellerVerify_Success covers the normal seller verification path over
// HTTP: order completes and the product is sold, while non-sellers are rejected.
func TestAPI_SellerVerify_Success(t *testing.T) {
	fx := newAPIFixture(t)
	defer fx.close()

	product := fx.createProduct(fx.sellerID, "接口测试-卖家核销")
	ord := fx.placeOrder(fx.buyerID, product.ID)
	_, env := fx.buyerConfirm(fx.buyerID, ord.ID)
	code := decodeOrder(t, env).HandoverCode

	// Permission: buyer and an outsider cannot verify.
	status, env := fx.verify(fx.buyerID, ord.ID, code)
	if status != http.StatusForbidden || env.Code != constants.CodeForbidden {
		t.Fatalf("buyer verify want 403/%d, got %d/%d", constants.CodeForbidden, status, env.Code)
	}
	status, env = fx.verify(fx.otherID, ord.ID, code)
	if status != http.StatusForbidden || env.Code != constants.CodeForbidden {
		t.Fatalf("outsider verify want 403/%d, got %d/%d", constants.CodeForbidden, status, env.Code)
	}

	// Anonymous verify is rejected by route auth.
	status, _ = fx.request(http.MethodPost, "/api/v1/trade-orders/"+itoa(ord.ID)+"/handover-code/verify", 0, map[string]any{"code": code})
	if status != http.StatusUnauthorized {
		t.Fatalf("anonymous verify want 401, got %d", status)
	}

	// Validation: code must be 6 digits (binding rule through the real stack).
	status, env = fx.verify(fx.sellerID, ord.ID, "12345")
	if status != http.StatusBadRequest || env.Code != constants.CodeValidation {
		t.Fatalf("5-digit code want 400/%d, got %d/%d msg=%s", constants.CodeValidation, status, env.Code, env.Message)
	}
	status, _ = fx.verify(fx.sellerID, ord.ID, "abcdef")
	if status != http.StatusBadRequest {
		t.Fatalf("non-numeric code want 400, got %d", status)
	}

	// Happy path.
	status, env = fx.verify(fx.sellerID, ord.ID, code)
	if status != http.StatusOK || env.Code != constants.CodeOK {
		t.Fatalf("verify want 200/0, got %d/%d msg=%s", status, env.Code, env.Message)
	}
	done := decodeOrder(t, env)
	if done.Status != constants.TradeStatusCompleted || done.HandoverStatus != constants.HandoverCodeUsed {
		t.Fatalf("post-verify state = %s/%s", done.Status, done.HandoverStatus)
	}
	if p := fx.reloadProductModel(product.ID); p.Status != constants.ProductStatusSold {
		t.Fatalf("product = %s, want sold", p.Status)
	}
}

// TestAPI_Verify_WrongCode covers wrong-code rejection over HTTP: the order
// stays confirmed, the product stays on sale and the code is not consumed.
func TestAPI_Verify_WrongCode(t *testing.T) {
	fx := newAPIFixture(t)
	defer fx.close()

	product := fx.createProduct(fx.sellerID, "接口测试-错误码")
	ord := fx.placeOrder(fx.buyerID, product.ID)
	_, env := fx.buyerConfirm(fx.buyerID, ord.ID)
	code := decodeOrder(t, env).HandoverCode
	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}

	status, env := fx.verify(fx.sellerID, ord.ID, wrong)
	if status != http.StatusConflict || env.Code != constants.CodeHandoverMismatch {
		t.Fatalf("wrong code want 409/%d, got %d/%d", constants.CodeHandoverMismatch, status, env.Code)
	}
	if env.Message != constants.MsgHandoverCodeMismatch {
		t.Fatalf("wrong-code message = %q", env.Message)
	}
	stored := fx.reloadOrderModel(ord.ID)
	if stored.Status != constants.TradeStatusConfirmed || stored.HandoverStatus != constants.HandoverCodeUnused || stored.HandoverUsedAt != nil {
		t.Fatalf("order changed after wrong code: %+v", stored)
	}
	if p := fx.reloadProductModel(product.ID); p.Status != constants.ProductStatusOnSale {
		t.Fatalf("product = %s, want on_sale", p.Status)
	}
}

// TestAPI_Verify_ExpiredCode covers expired-code rejection and buyer-only
// regeneration over HTTP, then completes with the new code.
func TestAPI_Verify_ExpiredCode(t *testing.T) {
	fx := newAPIFixture(t)
	defer fx.close()

	product := fx.createProduct(fx.sellerID, "接口测试-过期码")
	ord := fx.placeOrder(fx.buyerID, product.ID)
	_, env := fx.buyerConfirm(fx.buyerID, ord.ID)
	oldCode := decodeOrder(t, env).HandoverCode

	// Expire the code directly in the DB.
	if err := fx.db.Model(&model.TradeOrder{}).Where("id = ?", ord.ID).
		Update("handover_expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatalf("expire code: %v", err)
	}

	status, env := fx.verify(fx.sellerID, ord.ID, oldCode)
	if status != http.StatusConflict || env.Code != constants.CodeHandoverExpired {
		t.Fatalf("expired want 409/%d, got %d/%d msg=%s", constants.CodeHandoverExpired, status, env.Code, env.Message)
	}
	if m := fx.reloadOrderModel(ord.ID); m.HandoverStatus != constants.HandoverCodeExpired {
		t.Fatalf("handover status = %s, want expired", m.HandoverStatus)
	}

	// Regenerate permission: seller and outsider are forbidden.
	status, env = fx.regenerate(fx.sellerID, ord.ID)
	if status != http.StatusForbidden || env.Code != constants.CodeForbidden {
		t.Fatalf("seller regenerate want 403/%d, got %d/%d", constants.CodeForbidden, status, env.Code)
	}
	status, env = fx.regenerate(fx.otherID, ord.ID)
	if status != http.StatusForbidden || env.Code != constants.CodeForbidden {
		t.Fatalf("outsider regenerate want 403/%d, got %d/%d", constants.CodeForbidden, status, env.Code)
	}
	// Anonymous regenerate rejected by route auth.
	status, _ = fx.request(http.MethodPost, "/api/v1/trade-orders/"+itoa(ord.ID)+"/handover-code/regenerate", 0, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("anonymous regenerate want 401, got %d", status)
	}

	// Buyer regenerates; old code is dead, new code works.
	status, env = fx.regenerate(fx.buyerID, ord.ID)
	if status != http.StatusOK || env.Code != constants.CodeOK {
		t.Fatalf("buyer regenerate want 200/0, got %d/%d msg=%s", status, env.Code, env.Message)
	}
	newCode := decodeOrder(t, env).HandoverCode
	if newCode == oldCode || len(newCode) != 6 {
		t.Fatalf("bad regenerated code %q (old %q)", newCode, oldCode)
	}
	status, env = fx.verify(fx.sellerID, ord.ID, oldCode)
	if status != http.StatusConflict || env.Code != constants.CodeHandoverMismatch {
		t.Fatalf("old code after regen want 409/%d, got %d/%d", constants.CodeHandoverMismatch, status, env.Code)
	}
	status, env = fx.verify(fx.sellerID, ord.ID, newCode)
	if status != http.StatusOK || env.Code != constants.CodeOK {
		t.Fatalf("new code verify want 200/0, got %d/%d msg=%s", status, env.Code, env.Message)
	}
	if p := fx.reloadProductModel(product.ID); p.Status != constants.ProductStatusSold {
		t.Fatalf("product = %s, want sold", p.Status)
	}
}

// TestAPI_Verify_Duplicate covers duplicate verification over HTTP: the first
// correct code completes the order; replays return 40912 and change nothing.
func TestAPI_Verify_Duplicate(t *testing.T) {
	fx := newAPIFixture(t)
	defer fx.close()

	product := fx.createProduct(fx.sellerID, "接口测试-重复核销")
	ord := fx.placeOrder(fx.buyerID, product.ID)
	_, env := fx.buyerConfirm(fx.buyerID, ord.ID)
	code := decodeOrder(t, env).HandoverCode

	status, env := fx.verify(fx.sellerID, ord.ID, code)
	if status != http.StatusOK || env.Code != constants.CodeOK {
		t.Fatalf("first verify want 200/0, got %d/%d", status, env.Code)
	}
	status, env = fx.verify(fx.sellerID, ord.ID, code)
	if status != http.StatusConflict || env.Code != constants.CodeHandoverUsed {
		t.Fatalf("duplicate verify want 409/%d, got %d/%d msg=%s", constants.CodeHandoverUsed, status, env.Code, env.Message)
	}
	otherCode := "000000"
	if otherCode == code {
		otherCode = "111111"
	}
	status, env = fx.verify(fx.sellerID, ord.ID, otherCode)
	if status != http.StatusConflict || env.Code != constants.CodeHandoverUsed {
		t.Fatalf("guess on completed order want 409/%d, got %d/%d", constants.CodeHandoverUsed, status, env.Code)
	}
	stored := fx.reloadOrderModel(ord.ID)
	if stored.Status != constants.TradeStatusCompleted || stored.HandoverStatus != constants.HandoverCodeUsed {
		t.Fatalf("order drifted after duplicate: %+v", stored)
	}
}

// TestAPI_Verify_ConcurrentSingleWinner drives the real HTTP endpoint with
// concurrent correct-code requests and asserts exactly one completes the order.
func TestAPI_Verify_ConcurrentSingleWinner(t *testing.T) {
	fx := newAPIFixture(t)
	defer fx.close()

	// Serialize writes on one pooled connection so the conditional UPDATE sees
	// committed rows; the DB-level guard decides the single winner (equivalent
	// to InnoDB row locking in production).
	sqlDB, _ := fx.db.DB()
	sqlDB.SetMaxOpenConns(1)

	product := fx.createProduct(fx.sellerID, "接口测试-并发核销")
	ord := fx.placeOrder(fx.buyerID, product.ID)
	_, env := fx.buyerConfirm(fx.buyerID, ord.ID)
	code := decodeOrder(t, env).HandoverCode

	const n = 16
	var wg sync.WaitGroup
	var success int64
	var used int64
	var other int64
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			httpStatus, e := fx.verify(fx.sellerID, ord.ID, code)
			switch {
			case httpStatus == http.StatusOK && e.Code == constants.CodeOK:
				atomic.AddInt64(&success, 1)
			case e.Code == constants.CodeHandoverUsed:
				atomic.AddInt64(&used, 1)
			default:
				atomic.AddInt64(&other, 1)
			}
		}()
	}
	wg.Wait()

	if atomic.LoadInt64(&success) != 1 {
		t.Fatalf("exactly one success expected, got %d (used=%d other=%d)", success, used, other)
	}
	if atomic.LoadInt64(&used) != n-1 {
		t.Fatalf("the other %d attempts must be 40912, got used=%d other=%d", n-1, used, other)
	}
	if p := fx.reloadProductModel(product.ID); p.Status != constants.ProductStatusSold {
		t.Fatalf("product = %s, want sold", p.Status)
	}
}

// TestAPI_Verify_PendingOrderNoCode covers the state rule that a pending order
// has no usable handover code yet (40913).
func TestAPI_Verify_PendingOrderNoCode(t *testing.T) {
	fx := newAPIFixture(t)
	defer fx.close()

	product := fx.createProduct(fx.sellerID, "接口测试-待确认无码")
	ord := fx.placeOrder(fx.buyerID, product.ID)

	status, env := fx.verify(fx.sellerID, ord.ID, "123456")
	if status != http.StatusConflict || env.Code != constants.CodeHandoverNoCode {
		t.Fatalf("pending verify want 409/%d, got %d/%d msg=%s", constants.CodeHandoverNoCode, status, env.Code, env.Message)
	}
}

// TestAPI_UnknownOrder_404 keeps the not-found rule exercised through HTTP.
func TestAPI_UnknownOrder_404(t *testing.T) {
	fx := newAPIFixture(t)
	defer fx.close()

	if status, env := fx.buyerConfirm(fx.buyerID, 999999); status != http.StatusNotFound || env.Code != constants.CodeNotFound {
		t.Fatalf("unknown confirm want 404/%d, got %d/%d", constants.CodeNotFound, status, env.Code)
	}
	if status, env := fx.verify(fx.sellerID, 999999, "123456"); status != http.StatusNotFound || env.Code != constants.CodeNotFound {
		t.Fatalf("unknown verify want 404/%d, got %d/%d", constants.CodeNotFound, status, env.Code)
	}
}
