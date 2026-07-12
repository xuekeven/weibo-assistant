<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import {
  createRealtimeSocket,
  getMonitorStatus,
  listGroups,
  listMessages,
  listNotificationEvents,
  type Group,
  type Message,
  type MonitorStatus,
  type NotificationEvent,
  type RealtimeEvent
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
const showNotifications = ref(false)
const autoFollowMessages = ref(true)
const messageListRef = ref<HTMLElement | null>(null)
const realtimeStatus = ref('connecting')
const currentTime = ref(new Date())

const monitorStatusItems = [
  {
    key: 'network_listening',
    label: '微博群聊监听中',
    description: '已连接微博聊天页的网络通道，正在等待新消息。'
  },
  {
    key: 'page_ok',
    label: '微博聊天页正常',
    description: '页面可以访问，标题和地址探测正常。'
  },
  {
    key: 'browser_started',
    label: '浏览器已启动',
    description: '无头 Chrome 已打开，正在进入微博聊天页。'
  },
  {
    key: 'monitor_starting',
    label: '监控启动中',
    description: '监控进程正在启动，尚未完成页面监听。'
  },
  {
    key: 'server_started',
    label: '后端服务已启动',
    description: 'API 服务已启动，等待监控进程上报状态。'
  },
  {
    key: 'page_probe_failed',
    label: '页面探测失败',
    description: '暂时无法读取页面状态，需要查看 monitor 日志。'
  }
]

const monitorStatusLabels = Object.fromEntries(monitorStatusItems.map((item) => [item.key, item.label])) as Record<
  string,
  string
>

let refreshInFlight = false
let refreshAgain = false
let socket: WebSocket | undefined
let reconnectTimer: number | undefined
let clockTimer: number | undefined
let stopped = false

const selectedGroup = computed(() => {
  return groups.value.find((group) => group.id === selectedGroupId.value) || null
})

const orderedMessages = computed(() => {
  return [...messages.value].sort((a, b) => {
    return new Date(a.sentAt || a.capturedAt).getTime() - new Date(b.sentAt || b.capturedAt).getTime()
  })
})

const monitorIsFresh = computed(() => {
  const heartbeat = monitor.value?.lastHeartbeatAt
  const staleAfter = monitor.value?.staleAfterSecs || 90
  if (!heartbeat) return monitor.value?.fresh !== false
  const heartbeatTime = new Date(heartbeat).getTime()
  if (Number.isNaN(heartbeatTime)) return monitor.value?.fresh !== false
  return currentTime.value.getTime() - heartbeatTime <= staleAfter * 1000
})

const monitorTone = computed(() => {
  const status = monitor.value?.status || ''
  if (!monitorIsFresh.value) return 'bad'
  if (monitor.value?.lastError) return 'bad'
  if (status.includes('ok') || status.includes('enabled') || status.includes('started') || status.includes('listening')) return 'good'
  if (status.includes('starting')) return 'warn'
  return 'idle'
})

const monitorLabel = computed(() => {
  if (!monitorIsFresh.value) return '监控心跳已过期'
  const status = monitor.value?.status || ''
  return monitorStatusLabels[status] || (status ? `状态：${status}` : '状态未知')
})

const monitorStatusTooltip = computed(() => {
  return `共 ${monitorStatusItems.length} 种状态`
})

const monitorHeartbeatLabel = computed(() => {
  const value = monitor.value?.lastHeartbeatAt || monitor.value?.updatedAt
  const timestamp = value ? new Date(value).getTime() : Number.NaN
  const age = Number.isNaN(timestamp) ? monitor.value?.heartbeatAgeSecs : Math.max(0, Math.floor((currentTime.value.getTime() - timestamp) / 1000))
  return `最后监控：${formatTime(value)}${typeof age === 'number' && age >= 0 ? `（${age} 秒前）` : ''}`
})

const currentTimeLabel = computed(() => `当前时间：${formatTime(currentTime.value.toISOString())}`)

const visibleGroups = computed(() => groups.value.filter((group) => !group.weiboGroupId?.startsWith('test-')))

async function refreshAll() {
  if (refreshInFlight) {
    refreshAgain = true
    return
  }
  refreshInFlight = true
  loading.value = true
  try {
    do {
      refreshAgain = false
      await runRefresh()
    } while (refreshAgain)
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    loading.value = false
    refreshInFlight = false
  }
}

async function runRefresh() {
  error.value = ''
  const shouldFollow = autoFollowMessages.value || messages.value.length === 0
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
}

async function refreshMessages() {
  const result = await listMessages({
    groupId: selectedGroupId.value || undefined,
    weiboSenderId: targetSenderId.value.trim() || undefined,
    q: query.value.trim() || undefined,
    limit: 80
  })
  messages.value = result.items || []
}

async function selectGroup(group: Group) {
  selectedGroupId.value = group.id
  autoFollowMessages.value = true
  await refreshMessages()
  scrollMessagesToBottom()
}

function messageMatchesCurrentFilters(message: Message) {
  if (selectedGroupId.value && message.groupId !== selectedGroupId.value) return false
  const sender = targetSenderId.value.trim()
  if (sender && message.weiboSenderId !== sender) return false
  const keyword = query.value.trim().toLowerCase()
  if (keyword) {
    const haystack = `${message.content || ''} ${message.weiboMessageId || ''} ${message.senderName || ''}`.toLowerCase()
    if (!haystack.includes(keyword)) return false
  }
  return true
}

function upsertMessage(message: Message) {
  const shouldFollow = autoFollowMessages.value || messages.value.length === 0
  const index = messages.value.findIndex((item) => item.id === message.id)
  updateGroupFromMessage(message)
  if (!messageMatchesCurrentFilters(message)) {
    if (index >= 0) {
      messages.value.splice(index, 1)
    }
    return
  }
  if (index >= 0) {
    messages.value.splice(index, 1, message)
  } else {
    messages.value.push(message)
    messages.value = [...messages.value]
      .sort((a, b) => new Date(b.sentAt || b.capturedAt).getTime() - new Date(a.sentAt || a.capturedAt).getTime())
      .slice(0, 80)
  }
  if (shouldFollow) {
    scrollMessagesToBottom()
  }
}

function updateGroupFromMessage(message: Message) {
  const lastMessageAt = message.sentAt || message.capturedAt
  const index = groups.value.findIndex((group) => group.id === message.groupId)
  if (index >= 0) {
    groups.value.splice(index, 1, {
      ...groups.value[index],
      name: message.groupName || groups.value[index].name,
      weiboGroupId: message.weiboGroupId || groups.value[index].weiboGroupId,
      lastMessageAt,
      updatedAt: lastMessageAt
    })
  } else {
    groups.value.push({
      id: message.groupId,
      name: message.groupName || `群聊 ${message.groupId}`,
      weiboGroupId: message.weiboGroupId,
      lastMessageAt,
      updatedAt: lastMessageAt
    })
  }
  groups.value = [...groups.value].sort((a, b) => {
    return new Date(b.lastMessageAt || b.updatedAt).getTime() - new Date(a.lastMessageAt || a.updatedAt).getTime()
  })
}

function prependNotification(event: NotificationEvent) {
  const index = notifications.value.findIndex((item) => item.id === event.id)
  if (index >= 0) {
    notifications.value.splice(index, 1, event)
  } else {
    notifications.value.unshift(event)
    notifications.value = notifications.value.slice(0, 30)
  }
}

function handleRealtimeEvent(event: RealtimeEvent) {
  if ((event.type === 'message_created' || event.type === 'message_updated') && event.message) {
    upsertMessage(event.message)
    return
  }
  if (event.type === 'monitor_status_updated' && event.monitorStatus) {
    monitor.value = event.monitorStatus
    return
  }
  if (event.type === 'notification_event_created' && event.notificationEvent) {
    prependNotification(event.notificationEvent)
  }
}

function connectRealtime() {
  if (stopped) return
  if (socket && (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING)) return
  realtimeStatus.value = 'connecting'
  socket = createRealtimeSocket()
  socket.addEventListener('open', () => {
    realtimeStatus.value = 'connected'
  })
  socket.addEventListener('message', (event) => {
    try {
      handleRealtimeEvent(JSON.parse(event.data) as RealtimeEvent)
    } catch (err) {
      console.warn('parse realtime event failed', err)
    }
  })
  socket.addEventListener('close', () => {
    realtimeStatus.value = 'disconnected'
    if (stopped) return
    refreshAll()
    reconnectTimer = window.setTimeout(connectRealtime, 3000)
  })
  socket.addEventListener('error', () => {
    realtimeStatus.value = 'error'
  })
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
  if (status === 'retrying') return '重试中'
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
  clockTimer = window.setInterval(() => {
    currentTime.value = new Date()
  }, 1000)
  refreshAll()
  connectRealtime()
})

onUnmounted(() => {
  stopped = true
  if (reconnectTimer) window.clearTimeout(reconnectTimer)
  if (clockTimer) window.clearInterval(clockTimer)
  socket?.close()
})
</script>

<template>
  <main class="app-shell" :class="{ 'with-notify': showNotifications }">
    <aside class="group-pane">
      <div class="pane-head">
        <div>
          <h1>微博助手</h1>
          <p>{{ visibleGroups.length }} 个群聊</p>
        </div>
        <button class="icon-button" type="button" title="刷新" @click="refreshAll">↻</button>
      </div>

      <div class="monitor-strip" :class="monitorTone">
        <div class="monitor-copy">
          <span class="monitor-title-row">
            <span class="status-dot"></span>
            <strong>{{ monitorLabel }}</strong>
            <span class="status-help" tabindex="0" :aria-label="monitorStatusTooltip">
              ?
              <span class="status-tooltip" role="tooltip">
                <span class="status-tooltip-title">{{ monitorStatusTooltip }}</span>
                <span v-for="item in monitorStatusItems" :key="item.key" class="status-tooltip-row">
                  <span class="status-tooltip-heading">
                    <strong>{{ item.label }}</strong>
                    <span v-if="item.key === monitor?.status" class="status-current-tag">当前</span>
                  </span>
                  <span class="status-tooltip-desc">{{ item.description }}</span>
                </span>
              </span>
            </span>
          </span>
          <small class="monitor-time-line">{{ monitorHeartbeatLabel }}</small>
          <small class="monitor-time-line">{{ currentTimeLabel }}</small>
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
          </span>
          <time>{{ formatTime(group.lastMessageAt || group.updatedAt) }}</time>
        </button>
      </div>
    </aside>

    <section class="chat-pane">
      <header class="chat-head">
        <div>
          <h2>{{ selectedGroup?.name || '消息流' }}</h2>
        </div>
        <div class="head-actions">
          <button class="text-button" type="button" @click="showNotifications = !showNotifications">
            企微通知 {{ notifications.length }}
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
                <template
                  v-for="attachment in imageAttachments(message)"
                  :key="attachment.id || attachment.url || JSON.stringify(attachment.meta)"
                >
                  <a
                    v-if="attachment.url"
                    class="image-item"
                    :href="attachment.url"
                    target="_blank"
                    rel="noreferrer"
                  >
                    <img
                      :src="attachment.url"
                      alt="微博图片消息"
                      loading="lazy"
                      @load="handleMessageMediaLoaded"
                    />
                  </a>
                  <span v-else class="image-item pending">
                    <strong>图片待解析</strong>
                    <small v-if="attachmentFids(message).length > 0">fid {{ attachmentFids(message).join(', ') }}</small>
                  </span>
                </template>
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
    </section>

    <div v-if="showNotifications" class="notify-backdrop" @click="showNotifications = false"></div>
    <aside v-if="showNotifications" class="notify-pane">
      <div class="pane-head">
        <div>
          <h2>企微通知记录</h2>
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
