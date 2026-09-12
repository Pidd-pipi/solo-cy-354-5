package constants

// TradeStatus defines trade order state machine values shared with the frontend.
const (
	TradeStatusPending   = "pending"
	TradeStatusConfirmed = "confirmed"
	TradeStatusCompleted = "completed"
	TradeStatusCancelled = "cancelled"
)

// TradeStatuses lists all valid trade statuses in flow order.
var TradeStatuses = []string{
	TradeStatusPending, TradeStatusConfirmed, TradeStatusCompleted, TradeStatusCancelled,
}

// IsTradeStatus reports whether the given status is valid.
func IsTradeStatus(s string) bool {
	for _, v := range TradeStatuses {
		if v == s {
			return true
		}
	}
	return false
}

// TradeStatusText returns the Chinese label of a trade status.
func TradeStatusText(s string) string {
	switch s {
	case TradeStatusPending:
		return "待确认"
	case TradeStatusConfirmed:
		return "已确认"
	case TradeStatusCompleted:
		return "已完成"
	case TradeStatusCancelled:
		return "已取消"
	default:
		return "未知"
	}
}

// ReviewRating defines review rating enum values shared with the frontend.
const (
	ReviewRatingGood   = "good"
	ReviewRatingMedium = "medium"
	ReviewRatingBad    = "bad"
)

// HandoverCodeStatus defines the lifecycle states of the one-time in-person
// handover code, shared with the frontend.
const (
	HandoverCodeUnused   = "unused"   // 已生成、等待卖家核销
	HandoverCodeUsed     = "used"     // 已核销（订单完成）
	HandoverCodeExpired  = "expired"  // 超过有效期
	HandoverCodeCanceled = "canceled" // 订单取消，码作废
)

// HandoverCodeStatuses lists all valid handover code statuses.
var HandoverCodeStatuses = []string{
	HandoverCodeUnused, HandoverCodeUsed, HandoverCodeExpired, HandoverCodeCanceled,
}

// IsHandoverCodeStatus reports whether the given status is valid.
func IsHandoverCodeStatus(s string) bool {
	for _, v := range HandoverCodeStatuses {
		if v == s {
			return true
		}
	}
	return false
}

// HandoverCodeStatusText returns the Chinese label of a handover code status.
func HandoverCodeStatusText(s string) string {
	switch s {
	case HandoverCodeUnused:
		return "待核销"
	case HandoverCodeUsed:
		return "已核销"
	case HandoverCodeExpired:
		return "已过期"
	case HandoverCodeCanceled:
		return "已作废"
	default:
		return "无面交码"
	}
}

// ReviewRatingText returns the Chinese label of a review rating.
func ReviewRatingText(r string) string {
	switch r {
	case ReviewRatingGood:
		return "好评"
	case ReviewRatingMedium:
		return "中评"
	case ReviewRatingBad:
		return "差评"
	default:
		return "未知"
	}
}
