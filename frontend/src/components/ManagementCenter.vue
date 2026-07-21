<script setup lang="ts">
import {
  AlertOutlined,
  BarChartOutlined,
  BellOutlined,
  CloseOutlined,
  FileMarkdownOutlined,
  FileTextOutlined,
  HistoryOutlined,
  MessageOutlined,
  ProfileOutlined,
  TeamOutlined
} from '@ant-design/icons-vue'
import type { Group, HistorySyncJob, NotificationOutboxItem, NotificationRule } from '../api'
import { formatTime, notificationLabel } from '../utils/presentation'

export type ManagementTab = 'notifications' | 'rules' | 'history' | 'stats'
export type RuleForm = { name: string; groupId: string; senderId: string; messageType: string; enabled: boolean }
export type MessageStats = { total: number; senders: [string, number][]; types: [string, number][] }

defineProps<{
  outboxItems: NotificationOutboxItem[]
  notificationRules: NotificationRule[]
  actionableCount: number
  messageStats: MessageStats
  selectedGroup: Group | null
  historyJob: HistorySyncJob | null
  historySyncActive: boolean
  historySyncProgressing: boolean
}>()

const tab = defineModel<ManagementTab>('tab', { required: true })
defineEmits<{
  close: []
  retryAll: []
  retry: [item: NotificationOutboxItem]
  openRule: []
  editRule: [rule: NotificationRule]
  removeRule: [rule: NotificationRule]
  export: [format: 'csv' | 'json' | 'md']
  openHistory: []
}>()

function statShare(value: number, entries: [string, number][]) {
  const maximum = Math.max(1, ...entries.map((entry) => entry[1]))
  return `${Math.max(4, Math.round((value / maximum) * 100))}%`
}

function messageTypeLabel(type: string) {
  return ({ text: '文本', image: '图片', video: '视频', system: '系统消息', recall: '撤回', unknown: '其他' } as Record<string, string>)[type] || type
}

function historyJobLabel(job: HistorySyncJob, progressing: boolean) {
  if (job.status === 'pending') return '等待 monitor 执行'
  if (job.status === 'running') return progressing ? '正在回溯消息' : '回溯已暂停，等待 monitor 恢复'
  if (job.status === 'completed') return '上次回溯已完成'
  return '上次回溯失败'
}

function historyJobBadge(job: HistorySyncJob, progressing: boolean) {
  if (job.status === 'failed') return '失败'
  if (job.status === 'completed') return '完成'
  return progressing ? '进行中' : '已暂停'
}

function historyModeLabel(mode: HistorySyncJob['mode']) {
  return ({ startup: '启动补偿', recovery: '断线补漏', manual: '手动回溯' } as Record<HistorySyncJob['mode'], string>)[mode]
}
</script>

<template>
  <div class="notify-backdrop" @click="$emit('close')"></div>
  <aside class="notify-pane control-center">
    <div class="pane-head">
      <h2>管理中心</h2>
      <a-button class="panel-close-button" aria-label="关闭管理中心" @click="$emit('close')"><CloseOutlined /></a-button>
    </div>
    <div class="panel-tabs-wrap">
      <a-radio-group v-model:value="tab" class="panel-tabs" button-style="solid" aria-label="管理中心栏目">
        <a-radio-button value="notifications"><BellOutlined />通知</a-radio-button>
        <a-radio-button value="rules"><ProfileOutlined />规则</a-radio-button>
        <a-radio-button value="history"><HistoryOutlined />回溯</a-radio-button>
        <a-radio-button value="stats"><BarChartOutlined />统计</a-radio-button>
      </a-radio-group>
    </div>

    <div v-if="tab === 'notifications'" class="panel-content notify-list">
      <button v-if="actionableCount" class="retry-all" type="button" @click="$emit('retryAll')">重试全部失败任务</button>
      <article v-for="item in outboxItems" :key="`job-${item.id}`" class="notify-row" :class="item.status">
        <div><span class="notify-status"><strong>{{ notificationLabel(item.status) }}</strong><time>{{ formatTime(item.updatedAt) }}</time></span><span>消息 #{{ item.messageId }}</span></div>
        <template v-if="item.status === 'sent'">
          <p v-if="item.content === undefined" class="notify-data-warning">通知详情未返回，请重启后端服务后刷新。</p>
          <template v-else>
            <p class="notify-recipient"><b>接收人</b><span>{{ item.touser || '全员（@all）' }}</span></p>
            <p class="notify-content"><b>发送内容</b><span>{{ item.content }}</span></p>
          </template>
        </template>
        <p v-else class="notify-attempts">尝试 {{ item.attemptCount }}/{{ item.maxAttempts }}<template v-if="item.status === 'retry'"> · 下次 {{ formatTime(item.nextAttemptAt) }}</template></p>
        <p v-if="item.lastError">{{ item.lastError }}</p>
        <button v-if="item.status === 'failed' || item.status === 'retry'" type="button" @click="$emit('retry', item)">立即重试</button>
      </article>
      <div v-if="outboxItems.length === 0" class="panel-empty">暂无通知任务</div>
    </div>

    <div v-else-if="tab === 'rules'" class="panel-content rules-panel">
      <section class="history-panel-hero rule-panel-hero">
        <div>
          <span class="history-panel-eyebrow">当前群聊</span>
          <h3>{{ selectedGroup?.name || '未选择群聊' }}</h3>
          <p>{{ selectedGroup ? '可以为当前群聊创建发送人和消息类型通知规则。' : '可以创建跨群聊规则，也可以先在左侧选择一个群聊。' }}</p>
        </div>
        <button class="primary-action history-panel-action" type="button" @click="$emit('openRule')">新建通知规则</button>
      </section>
      <article v-for="rule in notificationRules" :key="rule.id" class="rule-card">
        <div><strong>{{ rule.name }}</strong><span :class="rule.enabled ? 'enabled' : 'disabled'">{{ rule.enabled ? '启用' : '停用' }}</span></div>
        <p>{{ rule.groupName || rule.weiboGroupId || '全部群聊' }} · {{ rule.senderNickname || rule.weiboSenderId || '全部发送人' }} · {{ rule.messageType || '全部类型' }}</p>
        <small>命中 {{ rule.hitCount || 0 }} · 失败 {{ rule.finalFailureCount || 0 }} · 最近 {{ formatTime(rule.lastMatchedAt) }}</small>
        <div class="card-actions"><button type="button" @click="$emit('editRule', rule)">编辑</button><button v-if="rule.name !== '环境变量默认通知规则'" type="button" @click="$emit('removeRule', rule)">删除</button><span v-else>由环境变量管理</span></div>
      </article>
    </div>

    <div v-else-if="tab === 'history'" class="panel-content history-panel">
      <section class="history-panel-hero">
        <div>
          <span class="history-panel-eyebrow">当前群聊</span>
          <h3>{{ selectedGroup?.name || '未选择群聊' }}</h3>
          <p>{{ selectedGroup ? '可以从这里查看回溯进度，也可以发起一次新的消息回溯。' : '请先在左侧选择一个群聊，再发起消息回溯。' }}</p>
        </div>
        <button class="primary-action history-panel-action" type="button" :disabled="!selectedGroup || historySyncActive" @click="$emit('openHistory')">
          {{ historySyncActive ? (historySyncProgressing ? '回溯中' : '回溯暂停') : '发起消息回溯' }}
        </button>
      </section>

      <section v-if="historyJob" class="history-job-card" :class="[historyJob.status, { paused: historySyncActive && !historySyncProgressing }]">
        <div class="history-job-card-head">
          <div>
            <span>{{ historyModeLabel(historyJob.mode) }}</span>
            <h3>{{ historyJobLabel(historyJob, historySyncProgressing) }}</h3>
          </div>
          <strong>{{ historyJobBadge(historyJob, historySyncProgressing) }}</strong>
        </div>

        <div class="history-job-metrics">
          <article><strong>{{ historyJob.pagesFetched }}</strong><span>已扫描页数</span></article>
          <article><strong>{{ historyJob.messagesSeen }}</strong><span>已看到消息</span></article>
          <article><strong>{{ historyJob.messagesInserted }}</strong><span>新增消息</span></article>
        </div>

        <dl class="history-job-detail">
          <div><dt>任务群聊</dt><dd>{{ historyJob.groupName || historyJob.weiboGroupId }}</dd></div>
          <div><dt>更新时间</dt><dd>{{ formatTime(historyJob.updatedAt) }}</dd></div>
          <div v-if="historyJob.requestedFrom"><dt>最早消息时间</dt><dd>{{ formatTime(historyJob.requestedFrom) }}</dd></div>
          <div v-if="historyJob.requestedTo"><dt>最晚消息时间</dt><dd>{{ formatTime(historyJob.requestedTo) }}</dd></div>
          <div v-if="historyJob.stopReason"><dt>停止原因</dt><dd>{{ historyJob.stopReason }}</dd></div>
          <div v-if="historyJob.lastError"><dt>错误信息</dt><dd>{{ historyJob.lastError }}</dd></div>
        </dl>
      </section>

      <div v-else class="panel-empty history-empty">
        <HistoryOutlined />
        <strong>暂无回溯任务</strong>
        <span>{{ selectedGroup ? '点击“发起消息回溯”后，这里会显示执行进度和结果。' : '选择群聊后，可以查看或发起该群聊的消息回溯。' }}</span>
      </div>
    </div>

    <div v-else class="panel-content stats-panel">
      <div class="stat-grid">
        <article class="stat-card messages"><span class="stat-icon"><MessageOutlined /></span><div><strong>{{ messageStats.total }}</strong><span>当前结果消息</span></div></article>
        <article class="stat-card senders"><span class="stat-icon"><TeamOutlined /></span><div><strong>{{ messageStats.senders.length }}</strong><span>活跃发送人</span></div></article>
        <article class="stat-card rules"><span class="stat-icon"><ProfileOutlined /></span><div><strong>{{ notificationRules.length }}</strong><span>通知规则</span></div></article>
        <article class="stat-card failures"><span class="stat-icon"><AlertOutlined /></span><div><strong>{{ actionableCount }}</strong><span>失败通知</span></div></article>
      </div>

      <section class="stats-section">
        <div class="stats-section-head"><h3>活跃发送人</h3><span>{{ messageStats.senders.length }} 人</span></div>
        <ol v-if="messageStats.senders.length" class="stats-ranking">
          <li v-for="entry in messageStats.senders" :key="entry[0]">
            <div class="stats-rank-line"><span :title="entry[0]">{{ entry[0] }}</span><strong>{{ entry[1] }}</strong></div>
            <div class="stats-track"><span :style="{ width: statShare(entry[1], messageStats.senders) }"></span></div>
          </li>
        </ol>
        <p v-else class="stats-empty">当前结果中暂无发送人</p>
      </section>

      <section class="stats-section">
        <div class="stats-section-head"><h3>消息类型</h3><span>{{ messageStats.types.length }} 类</span></div>
        <div v-if="messageStats.types.length" class="stats-types">
          <div v-for="entry in messageStats.types" :key="entry[0]" class="stats-type-row">
            <div class="stats-rank-line"><span>{{ messageTypeLabel(entry[0]) }}</span><strong>{{ entry[1] }}</strong></div>
            <div class="stats-track type-track"><span :style="{ width: statShare(entry[1], messageStats.types) }"></span></div>
          </div>
        </div>
        <p v-else class="stats-empty">当前结果中暂无消息</p>
      </section>

      <section class="stats-section export-section">
        <div class="stats-section-head"><h3>导出当前结果</h3></div>
        <div class="export-actions">
          <button type="button" @click="$emit('export', 'csv')"><FileTextOutlined /><span>CSV</span></button>
          <button type="button" @click="$emit('export', 'json')"><ProfileOutlined /><span>JSON</span></button>
          <button type="button" @click="$emit('export', 'md')"><FileMarkdownOutlined /><span>Markdown</span></button>
        </div>
      </section>
    </div>
  </aside>
</template>
