package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/lp/campus-market/internal/constants"
	"github.com/lp/campus-market/internal/dto"
	"github.com/lp/campus-market/internal/middleware"
	"github.com/lp/campus-market/internal/model"
	"github.com/lp/campus-market/internal/service"
	"github.com/lp/campus-market/internal/util"
)

// TradeOrderHandler exposes trade order endpoints.
type TradeOrderHandler struct {
	svc    *service.TradeOrderService
	users  *service.UserService
	logger *slog.Logger
}

// NewTradeOrderHandler wires the trade order handler dependencies.
func NewTradeOrderHandler(svc *service.TradeOrderService, users *service.UserService, logger *slog.Logger) *TradeOrderHandler {
	return &TradeOrderHandler{svc: svc, users: users, logger: logger}
}

// Create handles POST /trade-orders.
func (h *TradeOrderHandler) Create(c *gin.Context) {
	user := h.requireUser(c)
	if user == nil {
		return
	}
	var req dto.CreateTradeOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, constants.CodeValidation, constants.MsgValidationFailed)
		return
	}
	order, err := h.svc.Create(c.Request.Context(), user, &req)
	if err != nil {
		c.Error(err)
		return
	}
	util.OK(c, dto.NewTradeOrderResponse(order, user.ID))
}

// ListMy handles GET /trade-orders/me. The one-time handover code is only
// included on orders viewed by their buyer.
func (h *TradeOrderHandler) ListMy(c *gin.Context) {
	userID, err := middleware.CurrentUserID(c)
	if err != nil {
		util.Fail(c, http.StatusUnauthorized, constants.CodeUnauthorized, constants.MsgUnauthorized)
		return
	}
	var q dto.PageQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		util.Fail(c, http.StatusBadRequest, constants.CodeValidation, constants.MsgValidationFailed)
		return
	}
	result, err := h.svc.ListMy(c.Request.Context(), userID, &q)
	if err != nil {
		c.Error(err)
		return
	}
	items, _ := result.Items.([]model.TradeOrder)
	util.OK(c, &dto.PageResult{
		Items:    dto.NewTradeOrderResponseList(items, userID),
		Total:    result.Total,
		Page:     result.Page,
		PageSize: result.PageSize,
	})
}

// BuyerConfirm handles POST /trade-orders/:id/buyer-confirm and returns the
// freshly generated one-time handover code (buyer only).
func (h *TradeOrderHandler) BuyerConfirm(c *gin.Context) {
	h.act(c, h.svc.BuyerConfirm)
}

// RegenerateHandover handles POST /trade-orders/:id/handover-code/regenerate.
func (h *TradeOrderHandler) RegenerateHandover(c *gin.Context) {
	h.act(c, h.svc.RegenerateHandoverCode)
}

// VerifyHandover handles POST /trade-orders/:id/handover-code/verify (seller only).
func (h *TradeOrderHandler) VerifyHandover(c *gin.Context) {
	userID, err := middleware.CurrentUserID(c)
	if err != nil {
		util.Fail(c, http.StatusUnauthorized, constants.CodeUnauthorized, constants.MsgUnauthorized)
		return
	}
	orderID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "订单ID不合法")
		return
	}
	var req dto.VerifyHandoverCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, constants.CodeValidation, constants.MsgHandoverCodeFormat)
		return
	}
	order, err := h.svc.VerifyHandoverCode(c.Request.Context(), userID, uint(orderID), req.Code)
	if err != nil {
		c.Error(err)
		return
	}
	util.OK(c, dto.NewTradeOrderResponse(order, userID))
}

// Cancel handles POST /trade-orders/:id/cancel.
func (h *TradeOrderHandler) Cancel(c *gin.Context) {
	h.act(c, h.svc.Cancel)
}

func (h *TradeOrderHandler) act(c *gin.Context, fn func(ctx context.Context, userID, orderID uint) (*model.TradeOrder, error)) {
	userID, err := middleware.CurrentUserID(c)
	if err != nil {
		util.Fail(c, http.StatusUnauthorized, constants.CodeUnauthorized, constants.MsgUnauthorized)
		return
	}
	orderID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "订单ID不合法")
		return
	}
	order, err := fn(c.Request.Context(), userID, uint(orderID))
	if err != nil {
		c.Error(err)
		return
	}
	util.OK(c, dto.NewTradeOrderResponse(order, userID))
}

func (h *TradeOrderHandler) requireUser(c *gin.Context) *model.User {
	userID, err := middleware.CurrentUserID(c)
	if err != nil {
		util.Fail(c, http.StatusUnauthorized, constants.CodeUnauthorized, constants.MsgUnauthorized)
		return nil
	}
	user, err := h.users.GetProfile(c.Request.Context(), userID)
	if err != nil {
		c.Error(err)
		return nil
	}
	return user
}
