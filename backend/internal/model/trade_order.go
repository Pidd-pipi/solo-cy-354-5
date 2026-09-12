package model

import "time"

// TradeOrder is an order created when a buyer intends to purchase a product.
type TradeOrder struct {
	ID                 uint       `gorm:"primaryKey" json:"id"`
	ProductID          uint       `gorm:"index;not null" json:"product_id"`
	BuyerID            uint       `gorm:"index;not null" json:"buyer_id"`
	SellerID           uint       `gorm:"index;not null" json:"seller_id"`
	Status             string     `gorm:"size:16;index;not null;default:pending" json:"status"`
	BuyerConfirmedAt   *time.Time `json:"buyer_confirmed_at"`
	SellerConfirmedAt  *time.Time `json:"seller_confirmed_at"`
	CompletedAt        *time.Time `json:"completed_at"`
	HandoverCode       string     `gorm:"size:8;index" json:"handover_code,omitempty"`
	HandoverExpiresAt  *time.Time `json:"handover_expires_at"`
	HandoverUsedAt     *time.Time `json:"handover_used_at"`
	HandoverStatus     string     `gorm:"size:16;index;not null;default:''" json:"handover_status"`
	CreatedAt          time.Time  `json:"created_at"`
}
