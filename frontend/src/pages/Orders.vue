<template>
  <div class="page">
    <h2>我的交易</h2>
    <el-card v-for="o in orders" :key="o.id" class="order-card">
      <div class="order-row">
        <div>
          <TradeStatusBadge :status="o.status" />
          <span class="order-id">订单 #{{ o.id }} · 商品 #{{ o.product_id }}</span>
          <p class="order-meta">
            买家 #{{ o.buyer_id }} / 卖家 #{{ o.seller_id }} · {{ formatDateTime(o.created_at) }}
          </p>
        </div>
        <div class="order-actions">
          <el-button
            v-if="o.status === 'pending' && o.buyer_id === authStore.user?.id"
            size="small"
            type="primary"
            @click="buyerConfirmFn(o.id)"
          >
            确认订单并生成面交码
          </el-button>
          <el-button v-if="o.status === 'pending'" size="small" type="danger" @click="cancelFn(o.id)">取消</el-button>
          <el-button v-if="o.status === 'completed'" size="small" @click="reviewDialog(o)">评价</el-button>
        </div>
      </div>
      <!-- 面交核销：已确认订单买家看码、卖家输码；完成后保留核销结果反馈 -->
      <HandoverCodePanel
        v-if="(o.status === 'confirmed' || o.status === 'completed') && (o.buyer_id === authStore.user?.id || o.seller_id === authStore.user?.id)"
        :order="o"
        :current-user-id="authStore.user?.id"
        @verified="onOrderChanged"
        @regenerated="onOrderChanged"
      />
    </el-card>
    <el-empty v-if="orders.length === 0" description="暂无交易" />
    <el-dialog v-model="reviewVisible" title="信誉评价" width="420px">
      <el-form label-width="70px">
        <el-form-item label="评价">
          <el-select v-model="reviewForm.rating" style="width: 100%">
            <el-option v-for="r in REVIEW_RATINGS" :key="r.value" :label="r.label" :value="r.value" />
          </el-select>
        </el-form-item>
        <el-form-item label="内容">
          <el-input v-model="reviewForm.content" type="textarea" :rows="3" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="reviewVisible = false">取消</el-button>
        <el-button type="primary" @click="submitReview">提交</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { storeToRefs } from 'pinia'
import { ElMessage } from 'element-plus'
import TradeStatusBadge from '../components/common/TradeStatusBadge.vue'
import HandoverCodePanel from '../components/common/HandoverCodePanel.vue'
import { useTradeStore } from '../stores/tradeStore'
import { useAuthStore } from '../stores/authStore'
import { buyerConfirm, cancelTradeOrder } from '../api/tradeOrder'
import { createReview } from '../api/review'
import { REVIEW_RATINGS } from '../constants/trade'
import { formatDateTime } from '../utils/dateFormat'
import type { TradeOrder } from '../types'

const tradeStore = useTradeStore()
const { orders } = storeToRefs(tradeStore)
const { fetch } = tradeStore
const authStore = useAuthStore()
const reviewVisible = ref(false)
const reviewForm = reactive({ trade_id: 0, rating: 'good', content: '' })

async function buyerConfirmFn(id: number) {
  await buyerConfirm(id)
  ElMessage.success('订单已确认，一次性面交码已生成')
  await fetch()
}

async function cancelFn(id: number) {
  await cancelTradeOrder(id)
  ElMessage.success('已取消')
  await fetch()
}

async function onOrderChanged() {
  await fetch()
}

function reviewDialog(o: TradeOrder) {
  reviewForm.trade_id = o.id
  reviewForm.rating = 'good'
  reviewForm.content = ''
  reviewVisible.value = true
}

async function submitReview() {
  await createReview({ trade_id: reviewForm.trade_id, rating: reviewForm.rating, content: reviewForm.content })
  ElMessage.success('评价成功')
  reviewVisible.value = false
}

onMounted(fetch)
</script>

<style scoped>
.order-card {
  margin-bottom: 12px;
}
.order-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.order-id {
  margin-left: 8px;
  font-size: 13px;
  color: #606266;
}
.order-meta {
  color: #909399;
  font-size: 12px;
  margin: 6px 0 0;
}
</style>
