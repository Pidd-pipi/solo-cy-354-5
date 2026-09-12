import request from '../utils/request'
import type { PageResult, TradeOrder } from '../types'

export function createTradeOrder(product_id: number) {
  return request.post<never, { code: number; message: string; data: TradeOrder }>('/trade-orders', { product_id })
}

export function listMyOrders(params: { page?: number; page_size?: number }) {
  return request.get<never, { code: number; message: string; data: PageResult<TradeOrder> }>('/trade-orders/me', { params })
}

// 买家确认订单：后端同时生成一次性面交码（仅返回给买家）
export function buyerConfirm(id: number) {
  return request.post<never, { code: number; message: string; data: TradeOrder }>(`/trade-orders/${id}/buyer-confirm`)
}

// 买家重新生成一次性面交码（旧码立即失效）
export function regenerateHandoverCode(id: number) {
  return request.post<never, { code: number; message: string; data: TradeOrder }>(`/trade-orders/${id}/handover-code/regenerate`)
}

// 卖家现场输入买家出示的面交码进行核销
export function verifyHandoverCode(id: number, code: string) {
  return request.post<never, { code: number; message: string; data: TradeOrder }>(`/trade-orders/${id}/handover-code/verify`, { code })
}

export function cancelTradeOrder(id: number) {
  return request.post<never, { code: number; message: string; data: TradeOrder }>(`/trade-orders/${id}/cancel`)
}
