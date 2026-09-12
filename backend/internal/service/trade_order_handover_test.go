package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/lp/campus-market/internal/constants"
	"github.com/lp/campus-market/internal/dto"
	"github.com/lp/campus-market/internal/model"
	"github.com/lp/campus-market/internal/repository"
	"github.com/lp/campus-market/internal/util"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// handoverTTL is the handover code lifetime used by the tests.
const handoverTTL = 30 * time.Minute

// handoverFixture wires real repositories against an isolated, file-backed
// SQLite database. Each test gets its own file under t.TempDir(), so the suite
// is hermetic, needs no external MySQL and can be re-run repeatedly.
type handoverFixture struct {
	ctx    context.Context
	db     *gorm.DB
	svc    *TradeOrderService
	buyer  *model.User
	seller *model.User
	other  *model.User
}

func newHandoverFixture(t *testing.T) *handoverFixture {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "handover_test.db") + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(off)"
	gdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := gdb.AutoMigrate(
		&model.User{}, &model.Product{}, &model.Conversation{}, &model.Message{},
		&model.TradeOrder{}, &model.Review{}, &model.BookExchange{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	orderRepo := repository.NewTradeOrderRepository(gdb)
	productRepo := repository.NewProductRepository(gdb)
	svc := NewTradeOrderService(orderRepo, productRepo, int(handoverTTL/time.Minute), logger)

	fx := &handoverFixture{
		ctx: context.Background(),
		db:  gdb,
		svc: svc,
		buyer:  &model.User{Phone: "13000000001", Nickname: "买家", Role: constants.UserRoleStudent},
		seller: &model.User{Phone: "13000000002", Nickname: "卖家", Role: constants.UserRoleStudent},
		other:  &model.User{Phone: "13000000003", Nickname: "路人", Role: constants.UserRoleStudent},
	}
	for _, u := range []**model.User{&fx.buyer, &fx.seller, &fx.other} {
		if err := gdb.Create(*u).Error; err != nil {
			t.Fatalf("create user: %v", err)
		}
	}
	return fx
}

// createOnSaleProduct inserts an on-sale product owned by the seller.
func (f *handoverFixture) createOnSaleProduct(t *testing.T) *model.Product {
	t.Helper()
	p := &model.Product{
		SellerID: f.seller.ID, Title: "面交测试商品", Description: "test",
		Price: 12.5, Category: constants.ProductCategoryBooks, Condition: "全新",
		Campus: "东校区", TradeLocation: "东门", Status: constants.ProductStatusOnSale,
	}
	if err := f.db.Create(p).Error; err != nil {
		t.Fatalf("create product: %v", err)
	}
	return p
}

// placeAndConfirmOrder creates an order and runs buyer confirmation,
// returning the confirmed order (with the generated one-time code).
func (f *handoverFixture) placeAndConfirmOrder(t *testing.T) *model.TradeOrder {
	t.Helper()
	p := f.createOnSaleProduct(t)
	ord, err := f.svc.Create(f.ctx, f.buyer, &dto.CreateTradeOrderRequest{ProductID: p.ID})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	confirmed, err := f.svc.BuyerConfirm(f.ctx, f.buyer.ID, ord.ID)
	if err != nil {
		t.Fatalf("buyer confirm: %v", err)
	}
	return confirmed
}

func (f *handoverFixture) reloadOrder(t *testing.T, id uint) *model.TradeOrder {
	t.Helper()
	var o model.TradeOrder
	if err := f.db.First(&o, id).Error; err != nil {
		t.Fatalf("reload order %d: %v", id, err)
	}
	return &o
}

func (f *handoverFixture) reloadProduct(t *testing.T, id uint) *model.Product {
	t.Helper()
	var p model.Product
	if err := f.db.First(&p, id).Error; err != nil {
		t.Fatalf("reload product %d: %v", id, err)
	}
	return &p
}

// blockSoldStatusUpdate injects a fault exactly at the product status-update
// statement: a DB trigger aborts any transition of products.status to "sold".
// The product row and the order->product association are left intact, so the
// failure genuinely happens during the status update rather than because the
// product is missing/disassociated. Use unblockSoldStatusUpdate to clear it.
func (f *handoverFixture) blockSoldStatusUpdate(t *testing.T) {
	t.Helper()
	stmt := `
CREATE TRIGGER trg_fail_product_sold
BEFORE UPDATE ON products
FOR EACH ROW WHEN NEW.status = 'sold' AND OLD.status <> 'sold'
BEGIN
	SELECT RAISE(ABORT, 'simulated product status update failure');
END;`
	if err := f.db.Exec(stmt).Error; err != nil {
		t.Fatalf("create fail-sold trigger: %v", err)
	}
}

func (f *handoverFixture) unblockSoldStatusUpdate(t *testing.T) {
	t.Helper()
	if err := f.db.Exec("DROP TRIGGER IF EXISTS trg_fail_product_sold").Error; err != nil {
		t.Fatalf("drop fail-sold trigger: %v", err)
	}
}

// requireAppErr fails unless err is an AppError with the expected business code.
func requireAppErr(t *testing.T, err error, wantCode int) *util.AppError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %d, got nil", wantCode)
	}
	var ae *util.AppError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *AppError, got %T: %v", err, err)
	}
	if ae.Code != wantCode {
		t.Fatalf("expected code %d, got %d (%s)", wantCode, ae.Code, ae.Message)
	}
	return ae
}

// expectCode runs an order-returning service op and asserts its failure code.
func expectCode(t *testing.T, order *model.TradeOrder, err error, wantCode int) {
	t.Helper()
	if order != nil {
		t.Fatalf("expected nil order on failure, got #%d", order.ID)
	}
	requireAppErr(t, err, wantCode)
}

// TestHandoverCodeGeneratedOnBuyerConfirm verifies buyer confirmation mints a
// one-time, time-boxed code that is only visible to the buyer.
func TestHandoverCodeGeneratedOnBuyerConfirm(t *testing.T) {
	fx := newHandoverFixture(t)
	ord := fx.placeAndConfirmOrder(t)

	if ord.Status != constants.TradeStatusConfirmed {
		t.Fatalf("status = %s, want confirmed", ord.Status)
	}
	if len(ord.HandoverCode) != 6 {
		t.Fatalf("handover code = %q, want 6 digits", ord.HandoverCode)
	}
	for _, c := range ord.HandoverCode {
		if c < '0' || c > '9' {
			t.Fatalf("handover code %q must be numeric", ord.HandoverCode)
		}
	}
	if ord.HandoverStatus != constants.HandoverCodeUnused {
		t.Fatalf("handover status = %s, want unused", ord.HandoverStatus)
	}
	if ord.HandoverExpiresAt == nil {
		t.Fatalf("handover expiry not set")
	}
	if d := time.Until(*ord.HandoverExpiresAt); d < handoverTTL-time.Minute || d > handoverTTL+time.Minute {
		t.Fatalf("handover ttl = %v, want ~%v", d, handoverTTL)
	}
	if ord.BuyerConfirmedAt == nil {
		t.Fatalf("buyer_confirmed_at not set")
	}

	// The generated code is persisted.
	stored := fx.reloadOrder(t, ord.ID)
	if stored.HandoverCode != ord.HandoverCode || stored.HandoverStatus != constants.HandoverCodeUnused {
		t.Fatalf("code not persisted: %+v", stored)
	}

	// Role-aware visibility: buyer sees the code, nobody else does.
	if dto.NewTradeOrderResponse(stored, fx.buyer.ID).HandoverCode != ord.HandoverCode {
		t.Fatalf("buyer must see the handover code")
	}
	if dto.NewTradeOrderResponse(stored, fx.seller.ID).HandoverCode != "" {
		t.Fatalf("seller must not see the buyer handover code")
	}
	if dto.NewTradeOrderResponse(stored, fx.other.ID).HandoverCode != "" {
		t.Fatalf("third party must not see the handover code")
	}

	// Permission rule: only the buyer can confirm.
	o, err := fx.svc.BuyerConfirm(fx.ctx, fx.seller.ID, ord.ID)
	expectCode(t, o, err, constants.CodeForbidden)
	// State rule: confirmation is one-way (already confirmed).
	o, err = fx.svc.BuyerConfirm(fx.ctx, fx.buyer.ID, ord.ID)
	expectCode(t, o, err, constants.CodeConflict)
}

// TestHandoverCodeWrongCodeCannotComplete verifies a wrong code leaves both the
// order and the product untouched, while permission/state guards hold.
func TestHandoverCodeWrongCodeCannotComplete(t *testing.T) {
	fx := newHandoverFixture(t)
	ord := fx.placeAndConfirmOrder(t)
	wrong := "000000"
	if wrong == ord.HandoverCode {
		wrong = "111111"
	}

	o, err := fx.svc.VerifyHandoverCode(fx.ctx, fx.seller.ID, ord.ID, wrong)
	ae := requireAppErr(t, err, constants.CodeHandoverMismatch)
	if o != nil {
		t.Fatalf("failed verify must return nil order")
	}
	if ae.Message != constants.MsgHandoverCodeMismatch {
		t.Fatalf("mismatch message = %q", ae.Message)
	}

	// A failed attempt must not consume the code nor complete the order.
	stored := fx.reloadOrder(t, ord.ID)
	if stored.Status != constants.TradeStatusConfirmed || stored.HandoverStatus != constants.HandoverCodeUnused {
		t.Fatalf("order changed after wrong code: status=%s handover=%s", stored.Status, stored.HandoverStatus)
	}
	if stored.HandoverUsedAt != nil {
		t.Fatalf("handover_used_at must stay nil on failure, got %v", stored.HandoverUsedAt)
	}
	product := fx.reloadProduct(t, ord.ProductID)
	if product.Status != constants.ProductStatusOnSale {
		t.Fatalf("product status = %s, want on_sale", product.Status)
	}

	// Permission rules: buyer and unrelated users cannot verify.
	o, err = fx.svc.VerifyHandoverCode(fx.ctx, fx.buyer.ID, ord.ID, ord.HandoverCode)
	expectCode(t, o, err, constants.CodeForbidden)
	o, err = fx.svc.VerifyHandoverCode(fx.ctx, fx.other.ID, ord.ID, ord.HandoverCode)
	expectCode(t, o, err, constants.CodeForbidden)

	// State rule: a pending order has no usable code yet.
	p := fx.createOnSaleProduct(t)
	pending, cerr := fx.svc.Create(fx.ctx, fx.buyer, &dto.CreateTradeOrderRequest{ProductID: p.ID})
	if cerr != nil {
		t.Fatalf("create pending order: %v", cerr)
	}
	o, err = fx.svc.VerifyHandoverCode(fx.ctx, fx.seller.ID, pending.ID, "123456")
	expectCode(t, o, err, constants.CodeHandoverNoCode)
}

// TestHandoverCodeExpiredRejected verifies an expired code is rejected,
// marked expired, and that the buyer can regenerate and complete with the new code.
func TestHandoverCodeExpiredRejected(t *testing.T) {
	fx := newHandoverFixture(t)
	ord := fx.placeAndConfirmOrder(t)
	oldCode := ord.HandoverCode

	// Force the code past its expiry.
	if err := fx.db.Model(&model.TradeOrder{}).Where("id = ?", ord.ID).
		Update("handover_expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatalf("expire code: %v", err)
	}

	o, err := fx.svc.VerifyHandoverCode(fx.ctx, fx.seller.ID, ord.ID, oldCode)
	expectCode(t, o, err, constants.CodeHandoverExpired)
	stored := fx.reloadOrder(t, ord.ID)
	if stored.Status != constants.TradeStatusConfirmed || stored.HandoverStatus != constants.HandoverCodeExpired {
		t.Fatalf("after expiry want confirmed/expired, got %s/%s", stored.Status, stored.HandoverStatus)
	}
	if fx.reloadProduct(t, ord.ProductID).Status != constants.ProductStatusOnSale {
		t.Fatalf("product must stay on_sale after expired attempt")
	}

	// Only the buyer may regenerate; seller is forbidden.
	ro, rerr := fx.svc.RegenerateHandoverCode(fx.ctx, fx.seller.ID, ord.ID)
	expectCode(t, ro, rerr, constants.CodeForbidden)

	renewed, err := fx.svc.RegenerateHandoverCode(fx.ctx, fx.buyer.ID, ord.ID)
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if renewed.HandoverCode == oldCode {
		t.Fatalf("regenerated code must differ from the expired code")
	}
	if renewed.HandoverStatus != constants.HandoverCodeUnused || !renewed.HandoverExpiresAt.After(time.Now()) {
		t.Fatalf("renewed code not usable: %+v", renewed)
	}
	// The expired/old code is dead even though it once matched.
	o, err = fx.svc.VerifyHandoverCode(fx.ctx, fx.seller.ID, ord.ID, oldCode)
	expectCode(t, o, err, constants.CodeHandoverMismatch)
	// The new code completes the trade.
	done, err := fx.svc.VerifyHandoverCode(fx.ctx, fx.seller.ID, ord.ID, renewed.HandoverCode)
	if err != nil {
		t.Fatalf("verify renewed code: %v", err)
	}
	if done.Status != constants.TradeStatusCompleted {
		t.Fatalf("status = %s, want completed", done.Status)
	}
	if fx.reloadProduct(t, ord.ProductID).Status != constants.ProductStatusSold {
		t.Fatalf("product must be sold after verification")
	}
}

// TestHandoverCodeVerifySucceedsOnlyOnce verifies the one-time guarantee:
// the first correct verification completes the order, any later attempt fails.
func TestHandoverCodeVerifySucceedsOnlyOnce(t *testing.T) {
	fx := newHandoverFixture(t)
	ord := fx.placeAndConfirmOrder(t)
	code := ord.HandoverCode

	done, err := fx.svc.VerifyHandoverCode(fx.ctx, fx.seller.ID, ord.ID, code)
	if err != nil {
		t.Fatalf("first verification: %v", err)
	}
	if done.Status != constants.TradeStatusCompleted || done.HandoverStatus != constants.HandoverCodeUsed {
		t.Fatalf("unexpected post-verify state: %s/%s", done.Status, done.HandoverStatus)
	}
	if done.CompletedAt == nil || done.SellerConfirmedAt == nil || done.HandoverUsedAt == nil {
		t.Fatalf("completion timestamps not set: %+v", done)
	}
	if p := fx.reloadProduct(t, ord.ProductID); p.Status != constants.ProductStatusSold {
		t.Fatalf("product status = %s, want sold", p.Status)
	}

	// Replaying the same (correct) code is rejected as already used.
	o, err := fx.svc.VerifyHandoverCode(fx.ctx, fx.seller.ID, ord.ID, code)
	expectCode(t, o, err, constants.CodeHandoverUsed)
	// And guessing another code on a completed order is likewise rejected.
	otherCode := "000000"
	if otherCode == code {
		otherCode = "111111"
	}
	o, err = fx.svc.VerifyHandoverCode(fx.ctx, fx.seller.ID, ord.ID, otherCode)
	expectCode(t, o, err, constants.CodeHandoverUsed)
	// Nothing drifted after the rejected replays.
	stored := fx.reloadOrder(t, ord.ID)
	if stored.Status != constants.TradeStatusCompleted || stored.HandoverStatus != constants.HandoverCodeUsed {
		t.Fatalf("order drifted after duplicate attempt: %s/%s", stored.Status, stored.HandoverStatus)
	}

	// State rule preserved: a completed order cannot be cancelled.
	co, cerr := fx.svc.Cancel(fx.ctx, fx.buyer.ID, ord.ID)
	expectCode(t, co, cerr, constants.CodeConflict)
}

// TestHandoverVerificationRollsBackWhenProductUpdateFails proves the order is
// never marked completed when the product "sold" status update itself fails:
// the whole verification transaction (code consumption + completion) rolls
// back. The product row and its association with the order are kept intact;
// the fault is injected on the UPDATE-products statement via a DB trigger.
func TestHandoverVerificationRollsBackWhenProductUpdateFails(t *testing.T) {
	fx := newHandoverFixture(t)
	ord := fx.placeAndConfirmOrder(t)
	code := ord.HandoverCode

	// Make the product status transition to "sold" fail at the database
	// statement itself (product row and order association remain in place).
	fx.blockSoldStatusUpdate(t)
	defer fx.unblockSoldStatusUpdate(t)

	o, err := fx.svc.VerifyHandoverCode(fx.ctx, fx.seller.ID, ord.ID, code)
	expectCode(t, o, err, constants.CodeInternalError)

	// Atomicity: order not completed, code not consumed, timestamps not set.
	stored := fx.reloadOrder(t, ord.ID)
	if stored.Status != constants.TradeStatusConfirmed {
		t.Fatalf("order status = %s, want confirmed (rollback)", stored.Status)
	}
	if stored.HandoverStatus != constants.HandoverCodeUnused {
		t.Fatalf("handover status = %s, want unused (code must not be consumed)", stored.HandoverStatus)
	}
	if stored.CompletedAt != nil || stored.SellerConfirmedAt != nil || stored.HandoverUsedAt != nil {
		t.Fatalf("timestamps must be rolled back, got %+v", stored)
	}
	// Association intact and product still unsold.
	if stored.ProductID != ord.ProductID {
		t.Fatalf("order-product association must be preserved, got product_id=%d", stored.ProductID)
	}
	product := fx.reloadProduct(t, ord.ProductID)
	if product.Status != constants.ProductStatusOnSale {
		t.Fatalf("product status = %s, want on_sale (must not be sold)", product.Status)
	}

	// Wrong-code attempts still fail with mismatch while the fault is armed
	// (and must not consume the code either).
	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}
	wo, werr := fx.svc.VerifyHandoverCode(fx.ctx, fx.seller.ID, ord.ID, wrong)
	expectCode(t, wo, werr, constants.CodeHandoverMismatch)
	if s := fx.reloadOrder(t, ord.ID); s.HandoverStatus != constants.HandoverCodeUnused {
		t.Fatalf("code consumed after mismatch: %s", s.HandoverStatus)
	}

	// Lift the fault: the SAME order with the SAME (unconsumed) code now
	// completes normally, proving the one-time code survived the rollback and
	// the failure really was at the status-update step.
	fx.unblockSoldStatusUpdate(t)
	done, err := fx.svc.VerifyHandoverCode(fx.ctx, fx.seller.ID, ord.ID, code)
	if err != nil {
		t.Fatalf("retry after fault lifted: %v", err)
	}
	if done.Status != constants.TradeStatusCompleted || done.HandoverStatus != constants.HandoverCodeUsed {
		t.Fatalf("retry should complete: %s/%s", done.Status, done.HandoverStatus)
	}
	if fp := fx.reloadProduct(t, ord.ProductID); fp.Status != constants.ProductStatusSold {
		t.Fatalf("product status after retry = %s, want sold", fp.Status)
	}

	// Cancelled orders cannot be verified (state rule preserved).
	p2 := fx.createOnSaleProduct(t)
	cancelled, cerr := fx.svc.Create(fx.ctx, fx.buyer, &dto.CreateTradeOrderRequest{ProductID: p2.ID})
	if cerr != nil {
		t.Fatalf("create order: %v", cerr)
	}
	if _, err := fx.svc.Cancel(fx.ctx, fx.seller.ID, cancelled.ID); err != nil {
		t.Fatalf("cancel pending order: %v", err)
	}
	vo, verr := fx.svc.VerifyHandoverCode(fx.ctx, fx.seller.ID, cancelled.ID, "123456")
	expectCode(t, vo, verr, constants.CodeConflict)
}

// TestHandoverConcurrentVerificationSucceedsOnce verifies the one-time
// guarantee under a race: many concurrent verify calls with the correct code
// must result in exactly one completion and a single product "sold" transition.
func TestHandoverConcurrentVerificationSucceedsOnce(t *testing.T) {
	fx := newHandoverFixture(t)
	// Serialize writes on a single pooled connection so the conditional UPDATE
	// is evaluated against committed rows; the DB-level guard (not Go locks)
	// still decides the single winner. This mirrors row locking in MySQL.
	sqlDB, err := fx.db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	ord := fx.placeAndConfirmOrder(t)
	code := ord.HandoverCode

	const n = 16
	var wg sync.WaitGroup
	var success int64
	codeCounts := map[int]int{}
	var mu sync.Mutex
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, verr := fx.svc.VerifyHandoverCode(fx.ctx, fx.seller.ID, ord.ID, code)
			if verr == nil {
				atomic.AddInt64(&success, 1)
				return
			}
			var ae *util.AppError
			if errors.As(verr, &ae) {
				mu.Lock()
				codeCounts[ae.Code]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if atomic.LoadInt64(&success) != 1 {
		t.Fatalf("exactly one verification must succeed, got %d", success)
	}
	usedCount := codeCounts[constants.CodeHandoverUsed]
	if int64(usedCount) != n-1 {
		t.Fatalf("the other %d attempts must be CodeHandoverUsed, got %v", n-1, codeCounts)
	}

	stored := fx.reloadOrder(t, ord.ID)
	if stored.Status != constants.TradeStatusCompleted || stored.HandoverStatus != constants.HandoverCodeUsed {
		t.Fatalf("post-race state = %s/%s", stored.Status, stored.HandoverStatus)
	}
	// Product transitioned to sold exactly once; no double update.
	if p := fx.reloadProduct(t, ord.ProductID); p.Status != constants.ProductStatusSold {
		t.Fatalf("product status = %s, want sold", p.Status)
	}
}
