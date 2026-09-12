package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/lp/campus-market/internal/constants"
	"github.com/lp/campus-market/internal/dto"
	"github.com/lp/campus-market/internal/model"
	"github.com/lp/campus-market/internal/repository"
	"github.com/lp/campus-market/internal/util"
)

// TradeOrderService manages purchase intents, the one-time in-person handover
// code flow and order completion.
type TradeOrderService struct {
	orders      *repository.TradeOrderRepository
	products    *repository.ProductRepository
	logger      *slog.Logger
	handoverTTL time.Duration
}

// NewTradeOrderService wires the trade order service dependencies.
func NewTradeOrderService(orders *repository.TradeOrderRepository, products *repository.ProductRepository, handoverTTLMinutes int, logger *slog.Logger) *TradeOrderService {
	if handoverTTLMinutes <= 0 {
		handoverTTLMinutes = 30
	}
	return &TradeOrderService{
		orders:      orders,
		products:    products,
		logger:      logger,
		handoverTTL: time.Duration(handoverTTLMinutes) * time.Minute,
	}
}

// Create creates a pending trade order for an on-sale product.
func (s *TradeOrderService) Create(ctx context.Context, buyer *model.User, req *dto.CreateTradeOrderRequest) (*model.TradeOrder, error) {
	product, err := s.products.FindByID(ctx, req.ProductID)
	if err != nil {
		return nil, util.WrapAppError(fmt.Errorf("trade_order[buyer=%d] product lookup: %w", buyer.ID, err), 404, constants.CodeNotFound, constants.MsgNotFound)
	}
	if product.SellerID == buyer.ID {
		return nil, util.NewAppError(400, constants.CodeBadRequest, "不能购买自己的商品", nil)
	}
	if product.Status != constants.ProductStatusOnSale {
		return nil, util.NewAppError(409, constants.CodeConflict, constants.MsgProductNotOnSale, nil)
	}
	if existing, err := s.orders.FindByProductAndBuyer(ctx, req.ProductID, buyer.ID); err == nil && existing != nil {
		return nil, util.NewAppError(409, constants.CodeConflict, "您已对该商品下单", nil)
	}
	order := &model.TradeOrder{
		ProductID: req.ProductID, BuyerID: buyer.ID, SellerID: product.SellerID,
		Status: constants.TradeStatusPending,
	}
	if err := s.orders.Create(ctx, order); err != nil {
		return nil, util.WrapAppError(fmt.Errorf("trade_order[buyer=%d] create: %w", buyer.ID, err), 500, constants.CodeInternalError, constants.MsgInternalError)
	}
	s.logger.Info(fmt.Sprintf(constants.LogTradeOrderCreateSuccess, order.ID, req.ProductID))
	return order, nil
}

// ListMy returns the orders where the user participates.
func (s *TradeOrderService) ListMy(ctx context.Context, userID uint, q *dto.PageQuery) (*dto.PageResult, error) {
	q.Normalize()
	items, total, err := s.orders.ListByUser(ctx, userID, q.Page, q.PageSize)
	if err != nil {
		return nil, util.WrapAppError(fmt.Errorf("trade_order[user=%d] list: %w", userID, err), 500, constants.CodeInternalError, constants.MsgInternalError)
	}
	return &dto.PageResult{Items: items, Total: total, Page: q.Page, PageSize: q.PageSize}, nil
}

// BuyerConfirm marks the order confirmed by the buyer and generates the
// one-time in-person handover code shown only to the buyer.
func (s *TradeOrderService) BuyerConfirm(ctx context.Context, userID, orderID uint) (*model.TradeOrder, error) {
	order, err := s.orders.FindByID(ctx, orderID)
	if err != nil {
		return nil, util.WrapAppError(fmt.Errorf("trade_order[id=%d] buyer confirm find: %w", orderID, err), 404, constants.CodeNotFound, constants.MsgNotFound)
	}
	if order.BuyerID != userID {
		return nil, util.NewAppError(403, constants.CodeForbidden, constants.MsgNotParticipant, nil)
	}
	if order.Status != constants.TradeStatusPending {
		return nil, util.NewAppError(409, constants.CodeConflict, constants.MsgTradeStatusInvalid, nil)
	}
	return s.generateCode(ctx, order, false)
}

// RegenerateHandoverCode issues a new one-time handover code for a confirmed
// order while the current code is unused or already expired.
func (s *TradeOrderService) RegenerateHandoverCode(ctx context.Context, userID, orderID uint) (*model.TradeOrder, error) {
	order, err := s.orders.FindByID(ctx, orderID)
	if err != nil {
		return nil, util.WrapAppError(fmt.Errorf("trade_order[id=%d] handover regenerate find: %w", orderID, err), 404, constants.CodeNotFound, constants.MsgNotFound)
	}
	if order.BuyerID != userID {
		return nil, util.NewAppError(403, constants.CodeForbidden, constants.MsgNotBuyer, nil)
	}
	if order.Status != constants.TradeStatusConfirmed {
		return nil, util.NewAppError(409, constants.CodeConflict, constants.MsgTradeStatusInvalid, nil)
	}
	if order.HandoverStatus == constants.HandoverCodeUsed {
		return nil, util.NewAppError(409, constants.CodeHandoverUsed, constants.MsgHandoverCodeUsed, nil)
	}
	return s.generateCode(ctx, order, true)
}

func (s *TradeOrderService) generateCode(ctx context.Context, order *model.TradeOrder, regenerate bool) (*model.TradeOrder, error) {
	code, err := util.GenerateHandoverCode()
	if err != nil {
		return nil, util.WrapAppError(fmt.Errorf("trade_order[id=%d] handover code generate: %w", order.ID, err), 500, constants.CodeInternalError, constants.MsgInternalError)
	}
	now := time.Now()
	expiresAt := now.Add(s.handoverTTL)
	if regenerate {
		if err := s.orders.RegenerateHandoverCode(ctx, order.ID, code, expiresAt); err != nil {
			if errors.Is(err, util.ErrConflict) {
				return nil, util.NewAppError(409, constants.CodeConflict, constants.MsgTradeStatusInvalid, nil)
			}
			return nil, util.WrapAppError(fmt.Errorf("trade_order[id=%d] handover code regenerate: %w", order.ID, err), 500, constants.CodeInternalError, constants.MsgInternalError)
		}
	} else {
		if err := s.orders.UpdateBuyerConfirmed(ctx, order.ID, now, code, expiresAt); err != nil {
			if errors.Is(err, util.ErrConflict) {
				return nil, util.NewAppError(409, constants.CodeConflict, constants.MsgTradeStatusInvalid, nil)
			}
			return nil, util.WrapAppError(fmt.Errorf("trade_order[id=%d] buyer confirm: %w", order.ID, err), 500, constants.CodeInternalError, constants.MsgInternalError)
		}
	}
	s.logger.Info(fmt.Sprintf(constants.LogTradeOrderBuyerConfirmSuccess, order.ID))
	s.logger.Info(fmt.Sprintf(constants.LogHandoverCodeGenerateSuccess, order.ID, order.BuyerID, expiresAt.Format(time.RFC3339)))
	order.Status = constants.TradeStatusConfirmed
	order.BuyerConfirmedAt = &now
	order.HandoverCode = code
	order.HandoverExpiresAt = &expiresAt
	order.HandoverUsedAt = nil
	order.HandoverStatus = constants.HandoverCodeUnused
	return order, nil
}

// handoverFailure builds a logged, user-facing failure for a code attempt.
func (s *TradeOrderService) handoverFailure(orderID, sellerID uint, status, code int, msg, reason string) error {
	s.logger.Warn(fmt.Sprintf(constants.LogHandoverCodeVerifyFailed, orderID, sellerID, reason))
	return util.NewAppError(status, code, msg, nil)
}

// VerifyHandoverCode validates the one-time code the seller enters at the
// in-person meeting; on success the order completes and the product is sold.
func (s *TradeOrderService) VerifyHandoverCode(ctx context.Context, sellerID, orderID uint, code string) (*model.TradeOrder, error) {
	order, err := s.orders.FindByID(ctx, orderID)
	if err != nil {
		return nil, util.WrapAppError(fmt.Errorf("trade_order[id=%d] handover verify find: %w", orderID, err), 404, constants.CodeNotFound, constants.MsgNotFound)
	}
	if order.SellerID != sellerID {
		return nil, s.handoverFailure(orderID, sellerID, 403, constants.CodeForbidden, constants.MsgNotSeller, "not_seller")
	}
	// Status-mismatch guards: the code can only be consumed on a confirmed
	// order that is still waiting for the in-person handover.
	switch order.Status {
	case constants.TradeStatusConfirmed:
		// continue to code checks below
	case constants.TradeStatusPending:
		return nil, s.handoverFailure(orderID, sellerID, 409, constants.CodeHandoverNoCode, constants.MsgHandoverCodeNoCode, "order_pending")
	case constants.TradeStatusCompleted:
		return nil, s.handoverFailure(orderID, sellerID, 409, constants.CodeHandoverUsed, constants.MsgHandoverCodeUsed, "order_completed")
	case constants.TradeStatusCancelled:
		return nil, s.handoverFailure(orderID, sellerID, 409, constants.CodeConflict, "订单已取消，面交码已作废，无法核销", "order_cancelled")
	default:
		return nil, s.handoverFailure(orderID, sellerID, 409, constants.CodeConflict, constants.MsgTradeStatusInvalid, "unknown_status")
	}
	if order.HandoverCode == "" || order.HandoverStatus == "" {
		return nil, s.handoverFailure(orderID, sellerID, 409, constants.CodeHandoverNoCode, constants.MsgHandoverCodeNoCode, "no_code")
	}
	switch order.HandoverStatus {
	case constants.HandoverCodeUsed:
		return nil, s.handoverFailure(orderID, sellerID, 409, constants.CodeHandoverUsed, constants.MsgHandoverCodeUsed, "code_used")
	case constants.HandoverCodeCanceled:
		return nil, s.handoverFailure(orderID, sellerID, 409, constants.CodeConflict, "面交码已作废，无法核销", "code_canceled")
	}
	now := time.Now()
	if order.HandoverExpiresAt != nil && !order.HandoverExpiresAt.After(now) {
		if markErr := s.orders.MarkHandoverExpired(ctx, orderID); markErr != nil {
			s.logger.Error(fmt.Sprintf("trade_order[id=%d] mark handover expired: %v", orderID, markErr))
		}
		return nil, s.handoverFailure(orderID, sellerID, 409, constants.CodeHandoverExpired, constants.MsgHandoverCodeExpired, "code_expired")
	}
	if order.HandoverCode != code {
		return nil, s.handoverFailure(orderID, sellerID, 409, constants.CodeHandoverMismatch, constants.MsgHandoverCodeMismatch, "code_mismatch")
	}
	// All checks passed: consume the one-time code, complete the order and
	// mark the product sold atomically.
	if err := s.orders.Transaction(ctx, func(txCtx context.Context) error {
		if err := s.orders.ConsumeHandoverCode(txCtx, orderID, code, now); err != nil {
			return err
		}
		if err := s.orders.CompleteOrder(txCtx, orderID, now); err != nil {
			return err
		}
		if err := s.products.UpdateStatus(txCtx, order.ProductID, constants.ProductStatusSold); err != nil {
			return err
		}
		return nil
	}); err != nil {
		if errors.Is(err, util.ErrConflict) {
			// Race: the row no longer matches an unused, valid code — reload
			// to report the precise reason (expired/used/status changed).
			fresh, relErr := s.orders.FindByID(ctx, orderID)
			if relErr == nil {
				switch {
				case fresh.Status == constants.TradeStatusCompleted || fresh.HandoverStatus == constants.HandoverCodeUsed:
					return nil, s.handoverFailure(orderID, sellerID, 409, constants.CodeHandoverUsed, constants.MsgHandoverCodeUsed, "code_used_race")
				case fresh.HandoverExpiresAt != nil && !fresh.HandoverExpiresAt.After(time.Now()):
					return nil, s.handoverFailure(orderID, sellerID, 409, constants.CodeHandoverExpired, constants.MsgHandoverCodeExpired, "code_expired_race")
				case fresh.Status != constants.TradeStatusConfirmed:
					return nil, s.handoverFailure(orderID, sellerID, 409, constants.CodeConflict, constants.MsgTradeStatusInvalid, "status_changed_race")
				}
			}
			return nil, s.handoverFailure(orderID, sellerID, 409, constants.CodeHandoverMismatch, constants.MsgHandoverCodeMismatch, "consume_conflict")
		}
		s.logger.Error(fmt.Sprintf(constants.LogTradeOrderCompleteFailed, orderID, err))
		return nil, util.WrapAppError(fmt.Errorf("trade_order[id=%d] handover verify: %w", orderID, err), 500, constants.CodeInternalError, constants.MsgInternalError)
	}
	s.logger.Info(fmt.Sprintf(constants.LogHandoverCodeVerifySuccess, orderID, sellerID))
	s.logger.Info(fmt.Sprintf(constants.LogTradeOrderSellerConfirmSuccess, orderID))
	s.logger.Info(fmt.Sprintf(constants.LogTradeOrderCompleteSuccess, orderID, order.ProductID))
	order.Status = constants.TradeStatusCompleted
	order.SellerConfirmedAt = &now
	order.CompletedAt = &now
	order.HandoverStatus = constants.HandoverCodeUsed
	order.HandoverUsedAt = &now
	return order, nil
}

// Cancel cancels a pending order.
func (s *TradeOrderService) Cancel(ctx context.Context, userID, orderID uint) (*model.TradeOrder, error) {
	order, err := s.orders.FindByID(ctx, orderID)
	if err != nil {
		return nil, util.WrapAppError(fmt.Errorf("trade_order[id=%d] cancel find: %w", orderID, err), 404, constants.CodeNotFound, constants.MsgNotFound)
	}
	if order.BuyerID != userID && order.SellerID != userID {
		return nil, util.NewAppError(403, constants.CodeForbidden, constants.MsgNotParticipant, nil)
	}
	if order.Status != constants.TradeStatusPending {
		return nil, util.NewAppError(409, constants.CodeConflict, constants.MsgTradeStatusInvalid, nil)
	}
	if err := s.orders.CancelOrder(ctx, orderID); err != nil {
		if errors.Is(err, util.ErrConflict) {
			return nil, util.NewAppError(409, constants.CodeConflict, constants.MsgTradeStatusInvalid, nil)
		}
		return nil, util.WrapAppError(fmt.Errorf("trade_order[id=%d] cancel: %w", orderID, err), 500, constants.CodeInternalError, constants.MsgInternalError)
	}
	s.logger.Info(fmt.Sprintf(constants.LogTradeOrderCancelSuccess, orderID))
	if order.HandoverCode != "" {
		s.logger.Info(fmt.Sprintf(constants.LogHandoverCodeRevoked, orderID))
	}
	order.Status = constants.TradeStatusCancelled
	return order, nil
}
