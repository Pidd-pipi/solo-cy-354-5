package repository

import (
	"context"
	"time"

	"github.com/lp/campus-market/internal/model"
	"github.com/lp/campus-market/internal/util"
	"gorm.io/gorm"
)

// TradeOrderRepository persists trade order rows.
type TradeOrderRepository struct {
	db *gorm.DB
}

// NewTradeOrderRepository builds a TradeOrderRepository.
func NewTradeOrderRepository(db *gorm.DB) *TradeOrderRepository {
	return &TradeOrderRepository{db: db}
}

// Transaction runs fn inside a database transaction for cross-repository writes.
func (r *TradeOrderRepository) Transaction(ctx context.Context, fn func(txCtx context.Context) error) error {
	return Transaction(ctx, r.db, fn)
}

// Create inserts a new trade order.
func (r *TradeOrderRepository) Create(ctx context.Context, o *model.TradeOrder) error {
	return db(ctx, r.db).Create(o).Error
}

// FindByID returns a trade order by id.
func (r *TradeOrderRepository) FindByID(ctx context.Context, id uint) (*model.TradeOrder, error) {
	var o model.TradeOrder
	err := db(ctx, r.db).First(&o, id).Error
	if err != nil {
		return nil, normalizeError(err)
	}
	return &o, nil
}

// FindByProductAndBuyer returns an active order of a buyer for a product.
func (r *TradeOrderRepository) FindByProductAndBuyer(ctx context.Context, productID, buyerID uint) (*model.TradeOrder, error) {
	var o model.TradeOrder
	err := db(ctx, r.db).
		Where("product_id = ? AND buyer_id = ? AND status IN ?", productID, buyerID, []string{"pending", "confirmed"}).
		First(&o).Error
	if err != nil {
		return nil, normalizeError(err)
	}
	return &o, nil
}

// ListByUser returns orders where the user is buyer or seller.
func (r *TradeOrderRepository) ListByUser(ctx context.Context, userID uint, page, pageSize int) ([]model.TradeOrder, int64, error) {
	q := db(ctx, r.db).Model(&model.TradeOrder{}).Where("buyer_id = ? OR seller_id = ?", userID, userID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.TradeOrder
	err := q.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// UpdateStatus sets the order status.
func (r *TradeOrderRepository) UpdateStatus(ctx context.Context, id uint, status string) error {
	res := db(ctx, r.db).Model(&model.TradeOrder{}).Where("id = ?", id).Update("status", status)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return util.ErrNotFound
	}
	return nil
}

// UpdateBuyerConfirmed sets the buyer confirmation timestamp, moves the order
// to confirmed and attaches the freshly generated one-time handover code.
func (r *TradeOrderRepository) UpdateBuyerConfirmed(ctx context.Context, id uint, ts time.Time, code string, expiresAt time.Time) error {
	res := db(ctx, r.db).Model(&model.TradeOrder{}).Where("id = ? AND status = ?", id, "pending").
		Updates(map[string]interface{}{
			"buyer_confirmed_at":  ts,
			"status":             "confirmed",
			"handover_code":      code,
			"handover_expires_at": expiresAt,
			"handover_used_at":   nil,
			"handover_status":    "unused",
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return util.ErrConflict
	}
	return nil
}

// RegenerateHandoverCode replaces the handover code of a confirmed order,
// allowed while the current code is still unused or already expired.
func (r *TradeOrderRepository) RegenerateHandoverCode(ctx context.Context, id uint, code string, expiresAt time.Time) error {
	res := db(ctx, r.db).Model(&model.TradeOrder{}).
		Where("id = ? AND status = ? AND handover_status IN ?", id, "confirmed", []string{"unused", "expired"}).
		Updates(map[string]interface{}{
			"handover_code":      code,
			"handover_expires_at": expiresAt,
			"handover_used_at":   nil,
			"handover_status":    "unused",
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return util.ErrConflict
	}
	return nil
}

// ConsumeHandoverCode performs the atomic one-time consumption of a code:
// it only matches an unused, non-expired code of a confirmed order, so
// duplicate submissions and concurrent verifications cannot double-complete.
func (r *TradeOrderRepository) ConsumeHandoverCode(ctx context.Context, orderID uint, code string, now time.Time) error {
	res := db(ctx, r.db).Model(&model.TradeOrder{}).
		Where("id = ? AND status = ? AND handover_status = ? AND handover_code = ? AND handover_expires_at > ?",
			orderID, "confirmed", "unused", code, now).
		Updates(map[string]interface{}{
			"handover_status": "used",
			"handover_used_at": now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return util.ErrConflict
	}
	return nil
}

// CompleteOrder marks the confirmed order completed together with its
// confirmation/completion timestamps.
func (r *TradeOrderRepository) CompleteOrder(ctx context.Context, id uint, ts time.Time) error {
	res := db(ctx, r.db).Model(&model.TradeOrder{}).Where("id = ? AND status = ?", id, "confirmed").
		Updates(map[string]interface{}{
			"seller_confirmed_at": ts,
			"completed_at":        ts,
			"status":              "completed",
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return util.ErrConflict
	}
	return nil
}

// MarkHandoverExpired flips an unused code past its expiry to expired.
func (r *TradeOrderRepository) MarkHandoverExpired(ctx context.Context, id uint) error {
	return db(ctx, r.db).Model(&model.TradeOrder{}).
		Where("id = ? AND handover_status = ? AND handover_expires_at <= ?", id, "unused", time.Now()).
		Update("handover_status", "expired").Error
}

// CancelOrder cancels a pending order.
func (r *TradeOrderRepository) CancelOrder(ctx context.Context, id uint) error {
	res := db(ctx, r.db).Model(&model.TradeOrder{}).Where("id = ? AND status = ?", id, "pending").
		Update("status", "cancelled")
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return util.ErrConflict
	}
	return nil
}
