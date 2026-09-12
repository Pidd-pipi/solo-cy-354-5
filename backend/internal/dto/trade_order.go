package dto

import (
	"time"

	"github.com/lp/campus-market/internal/model"
)

// CreateTradeOrderRequest creates a purchase intent for a product.
type CreateTradeOrderRequest struct {
	ProductID uint `json:"product_id" binding:"required"`
}

// VerifyHandoverCodeRequest is submitted by the seller at the in-person meeting.
type VerifyHandoverCodeRequest struct {
	// Code is the 6-digit one-time handover code shown to the buyer.
	Code string `json:"code" binding:"required,len=6,numeric"`
}

// TradeOrderResponse is the role-aware view of a trade order. The one-time
// handover code is only included when the viewer is the buyer.
type TradeOrderResponse struct {
	ID                uint       `json:"id"`
	ProductID         uint       `json:"product_id"`
	BuyerID           uint       `json:"buyer_id"`
	SellerID          uint       `json:"seller_id"`
	Status            string     `json:"status"`
	BuyerConfirmedAt  *time.Time `json:"buyer_confirmed_at"`
	SellerConfirmedAt *time.Time `json:"seller_confirmed_at"`
	CompletedAt       *time.Time `json:"completed_at"`
	HandoverCode      string     `json:"handover_code"`
	HandoverExpiresAt *time.Time `json:"handover_expires_at"`
	HandoverUsedAt    *time.Time `json:"handover_used_at"`
	HandoverStatus    string     `json:"handover_status"`
	CreatedAt         time.Time  `json:"created_at"`
}

// NewTradeOrderResponse maps a model order to its API view, hiding the
// handover code from everyone except the buyer.
func NewTradeOrderResponse(o *model.TradeOrder, viewerID uint) *TradeOrderResponse {
	resp := &TradeOrderResponse{
		ID:                o.ID,
		ProductID:         o.ProductID,
		BuyerID:           o.BuyerID,
		SellerID:          o.SellerID,
		Status:            o.Status,
		BuyerConfirmedAt:  o.BuyerConfirmedAt,
		SellerConfirmedAt: o.SellerConfirmedAt,
		CompletedAt:       o.CompletedAt,
		HandoverExpiresAt: o.HandoverExpiresAt,
		HandoverUsedAt:    o.HandoverUsedAt,
		HandoverStatus:    o.HandoverStatus,
		CreatedAt:         o.CreatedAt,
	}
	// The one-time handover code is only ever shown to the buyer.
	if o.BuyerID == viewerID && o.HandoverStatus != "" {
		resp.HandoverCode = o.HandoverCode
	}
	return resp
}

// NewTradeOrderResponseList maps a page of orders for the given viewer.
func NewTradeOrderResponseList(items []model.TradeOrder, viewerID uint) []*TradeOrderResponse {
	out := make([]*TradeOrderResponse, 0, len(items))
	for i := range items {
		out = append(out, NewTradeOrderResponse(&items[i], viewerID))
	}
	return out
}
