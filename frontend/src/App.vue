<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import {
  getMonitorStatus,
  listGroups,
  listMessages,
  listNotificationEvents,
  type Group,
  type Message,
  type MonitorStatus,
  type NotificationEvent
} from './api'

const groups = ref<Group[]>([])
const messages = ref<Message[]>([])
const notifications = ref<NotificationEvent[]>([])
const monitor = ref<MonitorStatus | null>(null)
const selectedGroupId = ref<number | null>(null)
const query = ref('')
const targetSenderId = ref('')
const loading = ref(false)
const error = ref('')
const lastRefreshAt = ref<Date | null>(null)
const showNotifications = ref(false)
const autoFollowMessages = ref(true)
const messageListRef = ref<HTMLElement | null>(null)

let timer: number | undefined

const selectedGroup = computed(() => {
  return groups.value.find((group) => group.id === selectedGroupId.value) || null
})

const orderedMessages = computed(() => {
  return [...messages.value].sort((a, b) => {
    return new Date(a.sentAt || a.capturedAt).getTime() - new Date(b.sentAt || b.capturedAt).getTime()
  })
})

const monitorTone = computed(() => {
  const status = monitor.value?.status || ''
  if (monitor.value?.lastError) return 'bad'
  if (status.includes('ok') || status.includes('enabled') || status.includes('started')) return 'good'
  if (status.includes('starting')) return 'warn'
  return 'idle'
})

const visibleGroups = computed(() => groups.value.filter((group) => !group.weiboGroupId?.startsWith('test-')))

async function refreshAll() {
  loading.value = true
  error.value = ''
  const shouldFollow = autoFollowMessages.value || messages.value.length === 0
  try {
    const [groupResult, statusResult, notificationResult] = await Promise.all([
      listGroups(),
      getMonitorStatus(),
      listNotificationEvents()
    ])

    groups.value = groupResult.items || []
    monitor.value = statusResult
    notifications.value = notificationResult.items || []

    if (!selectedGroupId.value && visibleGroups.value.length > 0) {
      selectedGroupId.value = visibleGroups.value[0].id
    }
    await refreshMessages()
    if (shouldFollow) {
      scrollMessagesToBottom()
    }
    lastRefreshAt.value = new Date()
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    loading.value = false
  }
}

async function refreshMessages() {
  const result = await listMessages({
    groupId: selectedGroupId.value || undefined,
    weiboSenderId: targetSenderId.value.trim() || undefined,
    q: query.value.trim() || undefined,
    limit: 120
  })
  messages.value = result.items || []
}

async function selectGroup(group: Group) {
  selectedGroupId.value = group.id
  autoFollowMessages.value = true
  await refreshAll()
}

function isNearMessageBottom(threshold = 120) {
  const element = messageListRef.value
  if (!element) return true
  return element.scrollHeight - element.scrollTop - element.clientHeight <= threshold
}

function updateAutoFollow() {
  autoFollowMessages.value = isNearMessageBottom()
}

function scrollMessagesToBottom() {
  nextTick(() => {
    window.requestAnimationFrame(() => {
      const element = messageListRef.value
      if (!element) return
      element.scrollTop = element.scrollHeight
      autoFollowMessages.value = true
    })
  })
}

function handleMessageMediaLoaded() {
  if (autoFollowMessages.value) {
    scrollMessagesToBottom()
  }
}

function formatTime(value?: string) {
  if (!value) return '--'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false
  }).format(date)
}

function initials(name?: string) {
  const text = (name || '?').trim()
  return text.slice(0, 1).toUpperCase()
}

function notificationLabel(status: string) {
  if (status === 'sent') return '已发送'
  if (status === 'failed') return '失败'
  if (status === 'skipped') return '跳过'
  return status
}

function imageAttachments(message: Message) {
  return (message.attachments || []).filter((attachment) => attachment.attachmentType === 'image')
}

function attachmentFids(message: Message) {
  return imageAttachments(message)
    .flatMap((attachment) => {
      const fids = attachment.meta?.fids
      return Array.isArray(fids) ? fids.map((fid) => String(fid)) : []
    })
    .filter(Boolean)
}

onMounted(() => {
  refreshAll()
  timer = window.setInterval(refreshAll, 3000)
})

onUnmounted(() => {
  if (timer) window.clearInterval(timer)
})
</script>

<template>
  <main class="app-shell" :class="{ 'with-notify': showNotifications }">
    <aside class="group-pane">
      <div class="pane-head">
        <div>
          <h1>微博群聊监控</h1>
          <p>{{ visibleGroups.length }} 个群聊</p>
        </div>
        <button class="icon-button" type="button" title="刷新" @click="refreshAll">↻</button>
      </div>

      <div class="monitor-strip" :class="monitorTone">
        <span class="status-dot"></span>
        <div>
          <strong>{{ monitor?.status || 'unknown' }}</strong>
          <small>{{ formatTime(monitor?.lastHeartbeatAt || monitor?.updatedAt) }}</small>
        </div>
      </div>

      <div class="group-list">
        <button
          v-for="group in visibleGroups"
          :key="group.id"
          class="group-row"
          :class="{ active: group.id === selectedGroupId }"
          type="button"
          @click="selectGroup(group)"
        >
          <span class="avatar">{{ initials(group.name) }}</span>
          <span class="group-copy">
            <strong>{{ group.name }}</strong>
            <small>{{ group.weiboGroupId || `#${group.id}` }}</small>
          </span>
          <time>{{ formatTime(group.lastMessageAt || group.updatedAt) }}</time>
        </button>
      </div>
    </aside>

    <section class="chat-pane">
      <header class="chat-head">
        <div>
          <h2>{{ selectedGroup?.name || '消息流' }}</h2>
          <p>{{ selectedGroup?.weiboGroupId || '全部群聊' }}</p>
        </div>
        <div class="head-actions">
          <button class="text-button" type="button" @click="showNotifications = !showNotifications">
            通知 {{ notifications.length }}
          </button>
          <button class="text-button primary" type="button" @click="refreshAll">
            {{ loading ? '刷新中' : '刷新' }}
          </button>
        </div>
      </header>

      <div class="filters">
        <label>
          <span>关键词</span>
          <input v-model="query" type="search" placeholder="搜索消息" @keydown.enter="refreshAll" />
        </label>
        <label>
          <span>发送人 ID</span>
          <input v-model="targetSenderId" type="search" placeholder="weiboSenderId" @keydown.enter="refreshAll" />
        </label>
        <button type="button" @click="refreshAll">筛选</button>
      </div>

      <div v-if="error" class="error-line">{{ error }}</div>

      <div ref="messageListRef" class="message-list" @scroll.passive="updateAutoFollow">
        <div v-if="selectedGroup" class="timeline-mark">
          {{ selectedGroup.name }} · 最近 {{ orderedMessages.length }} 条
        </div>

        <article v-for="message in orderedMessages" :key="message.id" class="message-row">
          <span class="avatar sender">{{ initials(message.senderName) }}</span>
          <div class="message-main">
            <div class="message-meta">
              <strong>{{ message.senderName || message.weiboSenderId || '未知发送人' }}</strong>
              <span>{{ message.weiboSenderId }}</span>
              <time>{{ formatTime(message.sentAt || message.capturedAt) }}</time>
            </div>
            <div class="bubble" :class="{ media: imageAttachments(message).length > 0 }">
              <p v-if="message.content && (message.content !== '分享图片' || imageAttachments(message).length === 0)">
                {{ message.content }}
              </p>
              <div v-if="imageAttachments(message).length > 0" class="image-grid">
                <a
                  v-for="attachment in imageAttachments(message)"
                  :key="attachment.id || attachment.url || JSON.stringify(attachment.meta)"
                  class="image-item"
                  :class="{ pending: !attachment.url }"
                  :href="attachment.url"
                  target="_blank"
                  rel="noreferrer"
                >
                  <img
                    v-if="attachment.url"
                    :src="attachment.url"
                    alt="微博图片消息"
                    loading="lazy"
                    @load="handleMessageMediaLoaded"
                  />
                  <span v-else>
                    <strong>图片待解析</strong>
                    <small v-if="attachmentFids(message).length > 0">fid {{ attachmentFids(message).join(', ') }}</small>
                  </span>
                </a>
              </div>
              <p v-if="!message.content && imageAttachments(message).length === 0">[{{ message.messageType }}]</p>
            </div>
          </div>
        </article>

        <div v-if="orderedMessages.length === 0" class="empty-state">
          暂无消息
        </div>

        <button
          v-if="!autoFollowMessages && orderedMessages.length > 0"
          class="jump-bottom"
          type="button"
          @click="scrollMessagesToBottom"
        >
          回到底部
        </button>
      </div>

      <footer class="chat-foot">
        <span>最后刷新：{{ lastRefreshAt ? formatTime(lastRefreshAt.toISOString()) : '--' }}</span>
        <span>{{ orderedMessages.length }} 条消息</span>
      </footer>
    </section>

    <aside v-if="showNotifications" class="notify-pane">
      <div class="pane-head">
        <div>
          <h2>通知记录</h2>
          <p>最近 {{ notifications.length }} 条</p>
        </div>
        <button class="icon-button" type="button" title="关闭" @click="showNotifications = false">×</button>
      </div>

      <div class="notify-list">
        <article v-for="event in notifications" :key="event.id" class="notify-row" :class="event.status">
          <div>
            <strong>{{ notificationLabel(event.status) }}</strong>
            <span>消息 #{{ event.messageId }}</span>
          </div>
          <time>{{ formatTime(event.createdAt) }}</time>
          <p v-if="event.errorMessage">{{ event.errorMessage }}</p>
        </article>
      </div>
    </aside>
  </main>
</template>
