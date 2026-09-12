export const TRADE_STATUSES = [
  { value: 'pending', label: '待确认', type: 'warning' },
  { value: 'confirmed', label: '已确认', type: 'primary' },
  { value: 'completed', label: '已完成', type: 'success' },
  { value: 'cancelled', label: '已取消', type: 'info' },
] as const

export const REVIEW_RATINGS = [
  { value: 'good', label: '好评' },
  { value: 'medium', label: '中评' },
  { value: 'bad', label: '差评' },
] as const

// 一次性面交码状态：unused 待核销 / used 已核销 / expired 已过期 / canceled 已作废
export const HANDOVER_STATUSES = [
  { value: 'unused', label: '待核销', type: 'warning' },
  { value: 'used', label: '已核销', type: 'success' },
  { value: 'expired', label: '已过期', type: 'danger' },
  { value: 'canceled', label: '已作废', type: 'info' },
] as const

export function tradeStatusLabel(value: string): string {
  return TRADE_STATUSES.find((t) => t.value === value)?.label ?? value
}

export function tradeStatusType(value: string): string {
  return TRADE_STATUSES.find((t) => t.value === value)?.type ?? 'info'
}

export function handoverStatusLabel(value: string): string {
  return HANDOVER_STATUSES.find((s) => s.value === value)?.label ?? '无面交码'
}

export function handoverStatusType(value: string): string {
  return HANDOVER_STATUSES.find((s) => s.value === value)?.type ?? 'info'
}

export function ratingLabel(value: string): string {
  return REVIEW_RATINGS.find((r) => r.value === value)?.label ?? value
}
