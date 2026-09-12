<template>
  <div class="handover-panel" :class="`is-${effectiveStatus}`">
    <!-- 买家视图：面交码只对买家显示 -->
    <template v-if="isBuyer">
      <template v-if="order.handover_status && order.handover_code">
        <div class="handover-head">
          <el-tag :type="handoverTagType" size="small" effect="dark">{{ handoverStatusText }}</el-tag>
          <span class="handover-title">一次性面交码（仅你可见，现场向卖家出示）</span>
        </div>
        <div v-if="effectiveStatus === 'unused'" class="handover-code">{{ spacedCode }}</div>
        <div v-else class="handover-code handover-code--masked">••••••</div>
        <p class="handover-meta">
          有效期至：{{ formatDateTime(order.handover_expires_at ?? undefined) }}
          <span v-if="effectiveStatus === 'unused'" class="handover-countdown">（剩余 {{ countdownText }}）</span>
          <span v-else-if="effectiveStatus === 'expired'" class="handover-warn">（已过期，请重新生成）</span>
          <span v-else-if="effectiveStatus === 'used'">（已于 {{ formatDateTime(order.handover_used_at ?? undefined) }} 核销）</span>
        </p>
        <el-button
          v-if="effectiveStatus === 'unused' || effectiveStatus === 'expired'"
          size="small"
          :loading="regenerating"
          @click="onRegenerate"
        >
          {{ effectiveStatus === 'expired' ? '重新生成面交码' : '刷新面交码' }}
        </el-button>
        <span v-else class="handover-hint">面交码使用后立即失效，请勿提前透露给他人</span>
      </template>
      <p v-else class="handover-empty">确认订单后将在此生成一次性面交码。</p>
    </template>

    <!-- 卖家视图：输入买家现场出示的码 -->
    <template v-else-if="isSeller">
      <div class="handover-head">
        <el-tag v-if="order.handover_status" :type="handoverTagType" size="small">{{ handoverStatusText }}</el-tag>
        <span class="handover-title">面交核销</span>
      </div>
      <el-alert
        v-if="order.status === 'completed'"
        class="handover-feedback"
        title="面交码核销成功，交易完成，商品已售出"
        type="success"
        :closable="false"
        show-icon
      />
      <template v-else-if="order.status === 'confirmed'">
        <p class="handover-tip">请输入买家现场出示的 6 位面交码，校验成功后订单完成、商品售出。</p>
        <div class="handover-input-row">
          <el-input
            v-model="inputCode"
            class="handover-input"
            maxlength="6"
            placeholder="6 位数字面交码"
            :disabled="verifying"
            inputmode="numeric"
            @input="onCodeInput"
            @keyup.enter="onVerify"
          />
          <el-button type="success" :loading="verifying" @click="onVerify">核销</el-button>
        </div>
        <el-alert
          v-if="feedback.type"
          class="handover-feedback"
          :title="feedback.message"
          :type="feedback.type"
          :closable="true"
          show-icon
          @close="clearFeedback"
        />
      </template>
      <p v-else class="handover-empty">
        {{ order.status === 'pending' ? '买家确认订单后，可在此输入面交码核销。' : '该订单已取消，面交码作废。' }}
      </p>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import type { TradeOrder } from '../../types'
import { handoverStatusLabel, handoverStatusType } from '../../constants/trade'
import { regenerateHandoverCode, verifyHandoverCode } from '../../api/tradeOrder'
import { formatDateTime } from '../../utils/dateFormat'

const props = defineProps<{
  order: TradeOrder
  currentUserId?: number
}>()

const emit = defineEmits<{
  (e: 'verified', order: TradeOrder): void
  (e: 'regenerated', order: TradeOrder): void
}>()

const isBuyer = computed(() => props.currentUserId === props.order.buyer_id)
const isSeller = computed(() => props.currentUserId === props.order.seller_id)

// 过期在前端按当前时间实时判定，保证买家看到的状态与后端一致。
const nowTick = ref(Date.now())
let timer = window.setInterval(() => {
  nowTick.value = Date.now()
}, 1000)
onBeforeUnmount(() => window.clearInterval(timer))

const effectiveStatus = computed(() => {
  const s = props.order.handover_status
  if (s === 'unused' && props.order.handover_expires_at && new Date(props.order.handover_expires_at).getTime() <= nowTick.value) {
    return 'expired'
  }
  return s
})

const handoverStatusText = computed(() => handoverStatusLabel(effectiveStatus.value))
const handoverTagType = computed(() => handoverStatusType(effectiveStatus.value) as 'warning' | 'success' | 'danger' | 'info')

const spacedCode = computed(() => props.order.handover_code.replace(/(\d{3})(?=\d)/g, '$1 '))

const countdownText = computed(() => {
  if (!props.order.handover_expires_at) return '-'
  const ms = new Date(props.order.handover_expires_at).getTime() - nowTick.value
  if (ms <= 0) return '已过期'
  const total = Math.floor(ms / 1000)
  const m = Math.floor(total / 60)
  const sec = total % 60
  return `${m} 分 ${String(sec).padStart(2, '0')} 秒`
})

const regenerating = ref(false)
async function onRegenerate() {
  regenerating.value = true
  try {
    const res = await regenerateHandoverCode(props.order.id)
    emit('regenerated', res.data)
    ElMessage.success('已生成新的面交码，旧码已失效')
  } finally {
    regenerating.value = false
  }
}

const inputCode = ref('')
const verifying = ref(false)
const feedback = reactive<{ type: '' | 'success' | 'error' | 'warning'; message: string }>({ type: '', message: '' })

// reactive 对象不能整体重新赋值（const 绑定），只能逐字段清空，否则关闭按钮静默失效。
function clearFeedback() {
  feedback.type = ''
  feedback.message = ''
}

function onCodeInput(v: string) {
  inputCode.value = v.replace(/\D/g, '').slice(0, 6)
  // 卖家修改码时收起旧提示，避免连续试码时遮挡输入区。
  clearFeedback()
}

watch(
  () => props.order.id,
  () => {
    inputCode.value = ''
    feedback.type = ''
    feedback.message = ''
  },
)

async function onVerify() {
  if (inputCode.value.length !== 6) {
    feedback.type = 'warning'
    feedback.message = '请输入 6 位数字面交码'
    return
  }
  verifying.value = true
  feedback.type = ''
  feedback.message = ''
  try {
    const res = await verifyHandoverCode(props.order.id, inputCode.value)
    feedback.type = 'success'
    feedback.message = '核销成功，交易完成，商品已标记为售出'
    ElMessage.success('面交码核销成功，交易完成')
    inputCode.value = ''
    emit('verified', res.data)
  } catch (err: unknown) {
    // 后端已对错误/过期/重复/状态不符分别给出明确文案，拦截器也会弹全局提示；
    // 面板内再保留一条就地反馈，方便卖家对照输入框修改。
    feedback.type = 'error'
    feedback.message = (err as { response?: { data?: { message?: string } } })?.response?.data?.message ?? '核销失败，请稍后再试'
  } finally {
    verifying.value = false
  }
}
</script>

<style scoped>
.handover-panel {
  margin-top: 10px;
  padding: 10px 12px;
  border-radius: 8px;
  background: #f5f7fa;
  border: 1px dashed #dcdfe6;
}
.handover-panel.is-unused {
  background: #fdf6ec;
  border-color: #f5dab1;
}
.handover-panel.is-used {
  background: #f0f9eb;
  border-color: #c2e7b0;
}
.handover-panel.is-expired {
  background: #fef0f0;
  border-color: #fbc4c4;
}
.handover-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.handover-title {
  font-size: 13px;
  color: #303133;
  font-weight: 600;
}
.handover-code {
  font-size: 30px;
  font-weight: 700;
  letter-spacing: 6px;
  color: #e6a23c;
  margin: 8px 0 4px;
  font-variant-numeric: tabular-nums;
}
.handover-code--masked {
  color: #c0c4cc;
  letter-spacing: 4px;
}
.handover-meta {
  margin: 0;
  font-size: 12px;
  color: #606266;
}
.handover-countdown {
  color: #e6a23c;
}
.handover-warn {
  color: #f56c6c;
}
.handover-hint,
.handover-tip,
.handover-empty {
  font-size: 12px;
  color: #909399;
  margin: 8px 0 0;
}
.handover-input-row {
  display: flex;
  gap: 8px;
  margin-top: 8px;
}
.handover-input {
  max-width: 200px;
}
.handover-feedback {
  margin-top: 8px;
}
</style>
