<script setup lang="ts">
import { ArrowDownOutlined, HistoryOutlined, MenuOutlined, NotificationOutlined } from '@ant-design/icons-vue'
import { computed, defineAsyncComponent, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import GroupSidebar from './components/GroupSidebar.vue'
import MessageFilters from './components/MessageFilters.vue'
import MessageItem from './components/MessageItem.vue'
import type { ManagementTab, MessageExportRequest, RuleForm } from './components/ManagementCenter.vue'
import { useRealtimeConnection } from './composables/useRealtimeConnection'
import { formatTime } from './utils/presentation'
import { createLatestRequestGate } from './utils/requestGate'
import {
  createHistorySync,
  createNotificationRule,
  createRealtimeSocket,
  deleteNotificationRule,
  downloadGroupMessages,
  getAppConfig,
  getGroupDailyMessageStats,
  getMessage,
  getHistorySync,
  getReadiness,
  getMonitorStatus,
  listGroups,
  listMessages,
  listNotificationEvents,
  listNotificationOutbox,
  listNotificationRules,
  listUsers,
  retryNotificationOutbox,
  resolveMessageMedia,
  updateNotificationRule,
  type Group,
  type DailyMessageStats,
  type HistorySyncJob,
  type Message,
  type MonitorStatus,
  type NotificationEvent,
  type NotificationOutboxItem,
  type NotificationRule,
  type RealtimeEvent,
  type User
} from './api'

const HistorySyncDialog = defineAsyncComponent(() => import('./components/HistorySyncDialog.vue'))
const ManagementCenter = defineAsyncComponent(() => import('./components/ManagementCenter.vue'))
const MessageDetail = defineAsyncComponent(() => import('./components/MessageDetail.vue'))
const RuleDialog = defineAsyncComponent(() => import('./components/RuleDialog.vue'))

const groups = ref<Group[]>([])
const messages = ref<Message[]>([])
const notifications = ref<NotificationEvent[]>([])
const notificationRules = ref<NotificationRule[]>([])
const outboxItems = ref<NotificationOutboxItem[]>([])
const readiness = ref<Record<string, unknown>>({})
const appConfig = ref({ targetSenderId: '', currentUserId: '' })
const dailyMessageStats = ref<DailyMessageStats>({ today: 0, yesterday: 0 })
const users = ref<User[]>([])
const monitor = ref<MonitorStatus | null>(null)
const selectedGroupId = ref<number | null>(null)
const groupQuery = ref('')
const query = ref('')
const selectedSenderId = ref('')
const timeRange = ref('all')
const customDate = ref('')
const loading = ref(false)
const messagesLoading = ref(false)
const error = ref('')
const showNotifications = ref(false)
const panelTab = ref<ManagementTab>('notifications')
const selectedMessage = ref<Message | null>(null)
const loadingOlder = ref(false)
const hasOlderMessages = ref(true)
const historyDialogOpen = ref(false)
const historyFrom = ref('')
const historyTo = ref('')
const historyJob = ref<HistorySyncJob | null>(null)
const historySubmitting = ref(false)
const historyError = ref('')
const olderHistoryNotice = ref('')
const ruleForm = ref<RuleForm>({ name: '', groupId: '', senderId: '', messageType: '', enabled: true })
const editingRuleId = ref<number | null>(null)
const ruleDialogOpen = ref(false)
const panelError = ref('')
const previewImageURL = ref('')
const exportingMessages = ref(false)
const exportError = ref('')
const monitorExpanded = ref(true)
const mobileSidebarOpen = ref(false)
const sidebarWidth = ref(280)
const unreadByGroup = ref<Record<number, number>>({})
const newMessageCount = ref(0)
const autoFollowMessages = ref(true)
const messageListRef = ref<HTMLElement | null>(null)
const currentTime = ref(new Date())
const mediaResolutionRequests = new Set<number>()
const messageRequests = createLatestRequestGate()
const olderMessageRequests = createLatestRequestGate()
const historyRequests = createLatestRequestGate()
const detailRequests = createLatestRequestGate()
const allSelectValue = '__all__'
const antTheme = {
  token: {
    colorPrimary: '#17785d',
    borderRadius: 7,
    fontFamily: 'Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif'
  }
}

const timeRangeOptions = [
  { value: 'all', label: '全部时间' },
  { value: '24h', label: '最近 24 小时' },
  { value: '7d', label: '最近 7 天' },
  { value: '30d', label: '最近 30 天' },
  { value: 'custom', label: '指定日期以后' }
]

const senderOptions = computed(() => {
  const targetSenderId = appConfig.value.targetSenderId.trim()
  const options = [
    { label: '全部发送人', value: allSelectValue },
    ...users.value.filter((user) => user.weiboUserId).map((user) => ({ label: user.nickname || '未命名用户', value: user.weiboUserId || '' }))
  ]
  if (targetSenderId && !options.some((option) => option.value === targetSenderId)) {
    options.push({ label: `关注用户（${targetSenderId}）`, value: targetSenderId })
  }
  return options
})

// Reka UI 不允许 SelectItem 使用空字符串作为 value。界面层用哨兵值表示
// “全部”，读写业务状态时再转换回空字符串，避免影响 API 查询参数。
const senderSelectValue = computed({
  get: () => selectedSenderId.value || allSelectValue,
  set: (value: string) => { selectedSenderId.value = value === allSelectValue ? '' : value }
})

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
    key: 'login_required',
    label: '等待微博扫码登录',
    description: '请查看企业微信中的登录提示和二维码截图；扫码成功后会自动恢复监听。'
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
let clockTimer: number | undefined
let keywordSearchTimer: number | undefined
let lastAutoHistoryBoundary = ''

const selectedGroup = computed(() => {
  if (selectedGroupId.value === null) return null

  // PostgreSQL 的 bigint 在不同序列化链路中可能表现为 number 或 string。
  // 这里统一按字符串比较，避免左侧已经高亮，右侧却找不到当前群聊。
  return groups.value.find((group) => String(group.id) === String(selectedGroupId.value)) || null
})

const orderedMessages = computed(() => {
  return messages.value.filter(isDisplayableMessage).sort((a, b) => {
    return new Date(a.sentAt || a.capturedAt).getTime() - new Date(b.sentAt || b.capturedAt).getTime()
  })
})

const monitorIsFresh = computed(() => {
  const heartbeat = monitor.value?.lastHeartbeatAt
  const staleAfter = monitor.value?.staleAfterSecs || 180
  if (!heartbeat) return monitor.value?.fresh !== false
  const heartbeatTime = new Date(heartbeat).getTime()
  if (Number.isNaN(heartbeatTime)) return monitor.value?.fresh !== false
  return currentTime.value.getTime() - heartbeatTime <= staleAfter * 1000
})

const monitorTone = computed(() => {
  const status = monitor.value?.status || ''
  if (!monitorIsFresh.value) return 'bad'
  if (monitor.value?.lastError) return 'bad'
  if (status === 'login_required') return 'bad'
  if (status.includes('ok') || status.includes('enabled') || status.includes('started') || status.includes('listening')) return 'good'
  if (status.includes('starting')) return 'warn'
  return 'idle'
})

const monitorLabel = computed(() => {
  if (!monitorIsFresh.value) return '监控心跳已过期'
  const status = monitor.value?.status || ''
  return monitorStatusLabels[status] || (status ? `状态：${status}` : '状态未知')
})

const monitorAlertMessage = computed(() => {
  if (monitor.value?.lastError) return monitor.value.lastError
  return '开发环境请查看运行 monitor 命令的终端日志；systemd 部署时查看 logs/monitor.log。'
})

const monitorHeartbeatLabel = computed(() => {
  const value = monitor.value?.lastHeartbeatAt || monitor.value?.updatedAt
  const timestamp = value ? new Date(value).getTime() : Number.NaN
  const age = Number.isNaN(timestamp) ? monitor.value?.heartbeatAgeSecs : Math.max(0, Math.floor((currentTime.value.getTime() - timestamp) / 1000))
  return `最后监控：${formatTime(value)}${typeof age === 'number' && age >= 0 ? `（${age} 秒前）` : ''}`
})

const visibleGroups = computed(() => {
  const keyword = groupQuery.value.trim().toLowerCase()
  return groups.value.filter((group) => {
    if (group.weiboGroupId?.startsWith('test-')) return false
    return !keyword || `${group.name} ${group.lastMessagePreview || ''}`.toLowerCase().includes(keyword)
  })
})

const hasActiveFilters = computed(() => {
  return Boolean(query.value.trim() || selectedSenderId.value || timeRange.value !== 'all')
})

const historySyncActive = computed(() => historyJob.value?.status === 'pending' || historyJob.value?.status === 'running')
const historySyncProgressing = computed(() => historySyncActive.value && monitorIsFresh.value && !monitor.value?.lastError)
const historySyncBlocked = computed(() => historySyncActive.value && !historySyncProgressing.value)
const loadOlderLabel = computed(() => {
  if (historySyncActive.value) {
    return historySyncBlocked.value ? `回溯已暂停（已扫描 ${historyJob.value?.pagesFetched || 0} 页）` : `正在回溯消息（${historyJob.value?.pagesFetched || 0} 页）`
  }
  if (loadingOlder.value) return '正在加载…'
  if (olderHistoryNotice.value) return olderHistoryNotice.value
  return hasOlderMessages.value ? '加载更早消息' : '请求回溯更早消息'
})

const activeFilterLabels = computed(() => {
  const labels: Array<{ key: string; label: string }> = []
  if (query.value.trim()) labels.push({ key: 'query', label: `关键词：${query.value.trim()}` })
  if (selectedSenderId.value) {
    const user = users.value.find((item) => item.weiboUserId === selectedSenderId.value)
    labels.push({ key: 'sender', label: `发送人：${user?.nickname || selectedSenderId.value}` })
  }
  if (timeRange.value !== 'all') {
    labels.push({ key: 'time', label: timeRangeOptions.find((item) => item.value === timeRange.value)?.label || timeRange.value })
  }
  return labels
})

const realtimeBanner = computed(() => {
  if (realtimeStatus.value === 'connected') return null
  if (realtimeStatus.value === 'connecting') return { tone: 'info', text: '正在连接实时消息…' }
  if (realtimeStatus.value === 'error') return { tone: 'bad', text: '实时连接发生错误，正在尝试恢复。' }
  return { tone: 'warn', text: '实时连接已断开，正在重连；历史消息仍可查询。' }
})

const emptyMessage = computed(() => {
  if (!selectedGroup.value) return '请先从左侧选择一个群聊'
  if (hasActiveFilters.value) return '没有符合当前筛选条件的消息'
  return '这个群聊还没有采集到消息'
})

const actionableNotificationCount = computed(() => outboxItems.value.filter((item) => item.status === 'failed').length)

const selectedMessageNotifications = computed(() => {
  if (!selectedMessage.value) return []
  return notifications.value.filter((event) => event.messageId === selectedMessage.value?.id)
})

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
  const [groupResult, statusResult, notificationResult, userResult, ruleResult, outboxResult, readinessResult, configResult] = await Promise.all([
    listGroups(),
    getMonitorStatus(),
    listNotificationEvents(),
    listUsers(),
    listNotificationRules(),
    listNotificationOutbox(),
    getReadiness(),
    getAppConfig()
  ])

  groups.value = (groupResult.items || []).map(sanitizeGroup)
  monitor.value = statusResult
  notifications.value = notificationResult.items || []
  users.value = userResult.items || []
  notificationRules.value = ruleResult.items || []
  outboxItems.value = outboxResult.items || []
  readiness.value = readinessResult
  appConfig.value = {
    targetSenderId: configResult.targetSenderId || '',
    currentUserId: configResult.currentUserId || ''
  }

  if (!selectedGroupId.value && visibleGroups.value.length > 0) {
    selectedGroupId.value = visibleGroups.value[0].id
  }
  if (selectedGroupId.value) {
    await Promise.all([refreshMessages(), refreshHistorySync()])
  } else {
    messages.value = []
  }
  if (shouldFollow) {
    scrollMessagesToBottom()
  }
}

async function refreshMessages() {
  const requestKey = currentMessageRequestKey()
  const request = messageRequests.begin(requestKey)
  olderMessageRequests.invalidate()
  loadingOlder.value = false
  messagesLoading.value = true
  try {
    const statsRequest = refreshDailyMessageStats().catch((err) => {
      console.warn('refresh daily message stats failed', err)
    })
    const groupID = selectedGroupId.value || undefined
    const senderID = selectedSenderId.value || undefined
    const keyword = query.value.trim() || undefined
    const since = filterSince(timeRange.value)
    const result = await listMessages({
      groupId: groupID,
      weiboSenderId: senderID,
      q: keyword,
      since,
      limit: 50
    })
    await statsRequest
    if (!messageRequests.isCurrent(request, currentMessageRequestKey())) return
    const items = result.items || []
    const merged = new Map<number, Message>()
    for (const message of items.filter(isDisplayableMessage)) merged.set(message.id, message)
    for (const message of messages.value) {
      if (messageMatchesCurrentFilters(message)) merged.set(message.id, message)
    }
    messages.value = [...merged.values()]
      .sort((a, b) => new Date(b.sentAt || b.capturedAt).getTime() - new Date(a.sentAt || a.capturedAt).getTime())
      .slice(0, 80)
    hasOlderMessages.value = items.length === 50
  } finally {
    if (messageRequests.isCurrent(request, currentMessageRequestKey())) {
      messagesLoading.value = false
    }
  }
}

function currentMessageRequestKey() {
  return JSON.stringify({
    groupID: selectedGroupId.value,
    senderID: selectedSenderId.value,
    query: query.value.trim(),
    timeRange: timeRange.value,
    customDate: customDate.value
  })
}

function localDayBoundaries() {
  const today = new Date(currentTime.value)
  today.setHours(0, 0, 0, 0)
  const yesterday = new Date(today)
  yesterday.setDate(yesterday.getDate() - 1)
  const tomorrow = new Date(today)
  tomorrow.setDate(tomorrow.getDate() + 1)
  return {
    yesterdayStart: yesterday.toISOString(),
    todayStart: today.toISOString(),
    tomorrowStart: tomorrow.toISOString()
  }
}

async function refreshDailyMessageStats() {
  const groupID = selectedGroupId.value
  if (!groupID) {
    dailyMessageStats.value = { today: 0, yesterday: 0 }
    return
  }
  const stats = await getGroupDailyMessageStats(groupID, localDayBoundaries())
  if (String(groupID) === String(selectedGroupId.value)) dailyMessageStats.value = stats
}

async function refreshHistorySync() {
  const groupID = selectedGroupId.value
  const requestKey = String(groupID || '')
  const request = historyRequests.begin(requestKey)
  if (!groupID) {
    historyJob.value = null
    return
  }
  const job = (await getHistorySync(groupID)).job
  if (historyRequests.isCurrent(request, String(selectedGroupId.value || ''))) {
    historyJob.value = job
  }
}

async function applyFilters() {
  error.value = ''
  try {
    await refreshMessages()
    scrollMessagesToBottom()
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  }
}

async function applySenderFilter() {
  await applyFilters()
}

async function viewTargetSender() {
  const targetSenderId = appConfig.value.targetSenderId.trim()
  if (!targetSenderId) return
  selectedSenderId.value = targetSenderId
  await applyFilters()
}

async function clearFilters() {
  query.value = ''
  selectedSenderId.value = ''
  timeRange.value = 'all'
  await applyFilters()
}

async function clearFilter(key: string) {
  if (key === 'query') query.value = ''
  if (key === 'sender') selectedSenderId.value = ''
  if (key === 'time') timeRange.value = 'all'
  await applyFilters()
}

async function selectGroup(group: Group) {
  selectedGroupId.value = group.id
  messages.value = []
  historyJob.value = null
  messageRequests.invalidate()
  olderMessageRequests.invalidate()
  historyRequests.invalidate()
  unreadByGroup.value[group.id] = 0
  newMessageCount.value = 0
  updateDocumentTitle()
  mobileSidebarOpen.value = false
  autoFollowMessages.value = true
  lastAutoHistoryBoundary = ''
  error.value = ''
  try {
    await Promise.all([refreshMessages(), refreshHistorySync()])
    scrollMessagesToBottom()
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  }
}

function messageMatchesCurrentFilters(message: Message) {
  if (selectedGroupId.value && String(message.groupId) !== String(selectedGroupId.value)) return false
  const sender = selectedSenderId.value
  if (sender && (message.messageType === 'system' || message.messageType === 'event' || message.weiboSenderId !== sender)) return false
  const since = filterSince(timeRange.value)
  if (since && new Date(message.sentAt || message.capturedAt).getTime() < new Date(since).getTime()) return false
  const keyword = query.value.trim().toLowerCase()
  if (keyword) {
    const haystack = (message.content || '').trim().toLowerCase()
    if (haystack !== keyword) return false
  }
  return true
}

function isDisplayableMessage(message: Message) {
  if (message.messageType === 'event') return false
  return !/^微博群聊事件（类型 \d+）$/.test((message.content || '').trim())
}

function isCountableMemberMessage(message: Message) {
  if (!/^\d+$/.test((message.weiboSenderId || '').trim())) return false
  if (message.messageType === 'system' || message.messageType === 'event' || message.messageType === 'recall') return false
  return !/^微博群聊事件（类型 \d+）$/.test((message.content || '').trim())
}

function sanitizeGroup(group: Group): Group {
  if (/^微博群聊事件（类型 \d+）$/.test((group.lastMessagePreview || '').trim())) {
    return { ...group, lastMessagePreview: '' }
  }
  return group
}

function upsertMessage(message: Message) {
  if (!messageHasUnresolvedImage(message)) {
    mediaResolutionRequests.delete(message.id)
  }
  const index = messages.value.findIndex((item) => item.id === message.id)
  if (!isDisplayableMessage(message)) {
    if (index >= 0) messages.value.splice(index, 1)
    return
  }
  const shouldFollow = autoFollowMessages.value || messages.value.length === 0
  updateGroupFromMessage(message)
  if (String(message.groupId) !== String(selectedGroupId.value)) {
    unreadByGroup.value[message.groupId] = (unreadByGroup.value[message.groupId] || 0) + 1
    updateDocumentTitle()
  } else if (!shouldFollow) {
    newMessageCount.value++
    unreadByGroup.value[message.groupId] = newMessageCount.value
    updateDocumentTitle()
  }
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

function messageHasUnresolvedImage(message: Message) {
  return (message.attachments || []).some((attachment) =>
    attachment.attachmentType === 'image' &&
    !(attachment.url || '').trim() &&
    Array.isArray(attachment.meta?.fids) &&
    attachment.meta.fids.length > 0
  )
}

async function requestMessageMediaResolution(message: Message) {
  if (!monitorIsFresh.value || !messageHasUnresolvedImage(message) || mediaResolutionRequests.has(message.id)) return
  mediaResolutionRequests.add(message.id)
  try {
    await resolveMessageMedia(message.id)
  } catch (err) {
    mediaResolutionRequests.delete(message.id)
    console.warn('request media resolution failed', err)
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
      lastMessagePreview: message.content || `[${message.messageType}]`,
      updatedAt: lastMessageAt
    })
  } else {
    groups.value.push({
      id: message.groupId,
      name: message.groupName || `群聊 ${message.groupId}`,
      weiboGroupId: message.weiboGroupId,
      lastMessageAt,
      lastMessagePreview: message.content || `[${message.messageType}]`,
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
    if (event.type === 'message_created' && String(event.message.groupId) === String(selectedGroupId.value) && isCountableMemberMessage(event.message)) {
      const timestamp = new Date(event.message.sentAt || event.message.capturedAt).getTime()
      const boundaries = localDayBoundaries()
      if (timestamp >= new Date(boundaries.todayStart).getTime() && timestamp < new Date(boundaries.tomorrowStart).getTime()) {
        dailyMessageStats.value.today++
      } else if (timestamp >= new Date(boundaries.yesterdayStart).getTime() && timestamp < new Date(boundaries.todayStart).getTime()) {
        dailyMessageStats.value.yesterday++
      }
    }
    upsertMessage(event.message)
    return
  }
  if (event.type === 'monitor_status_updated' && event.monitorStatus) {
    monitor.value = event.monitorStatus
    return
  }
  if (event.type === 'notification_event_created' && event.notificationEvent) {
    prependNotification(event.notificationEvent)
    return
  }
  if (event.type === 'history_sync_updated' && event.historySyncJob && String(event.historySyncJob.groupId) === String(selectedGroupId.value)) {
    const previousStatus = historyJob.value?.status
    historyJob.value = event.historySyncJob
    if (event.historySyncJob.status === 'completed' && previousStatus !== 'completed' && event.historySyncJob.messagesInserted > 0) {
      olderHistoryNotice.value = ''
      hasOlderMessages.value = true
      void loadOlderMessages(false)
      void refreshDailyMessageStats()
    } else if (event.historySyncJob.status === 'completed') {
      olderHistoryNotice.value = '没有新增更早消息'
    }
  }
}

const {
  status: realtimeStatus,
  connect: connectRealtime
} = useRealtimeConnection(createRealtimeSocket, handleRealtimeEvent, () => {
  void refreshAll()
})

function isNearMessageBottom(threshold = 120) {
  const element = messageListRef.value
  if (!element) return true
  return element.scrollHeight - element.scrollTop - element.clientHeight <= threshold
}

function updateAutoFollow() {
  if ((messageListRef.value?.scrollTop || 0) < 80) void loadOlderMessages()
  autoFollowMessages.value = isNearMessageBottom()
  if (autoFollowMessages.value && selectedGroupId.value) {
    newMessageCount.value = 0
    unreadByGroup.value[selectedGroupId.value] = 0
    updateDocumentTitle()
  }
}

function scrollMessagesToBottom() {
  nextTick(() => {
    window.requestAnimationFrame(() => {
      const element = messageListRef.value
      if (!element) return
      element.scrollTop = element.scrollHeight
      autoFollowMessages.value = true
      newMessageCount.value = 0
      if (selectedGroupId.value) unreadByGroup.value[selectedGroupId.value] = 0
      updateDocumentTitle()
    })
  })
}

function handleMessageMediaLoaded() {
  if (autoFollowMessages.value) {
    scrollMessagesToBottom()
  }
}

function filterSince(range: string) {
  const durations: Record<string, number> = {
    '24h': 24 * 60 * 60 * 1000,
    '7d': 7 * 24 * 60 * 60 * 1000,
    '30d': 30 * 24 * 60 * 60 * 1000
  }
  if (range === 'custom' && customDate.value) return new Date(`${customDate.value}T00:00:00`).toISOString()
  const duration = durations[range]
  return duration ? new Date(Date.now() - duration).toISOString() : undefined
}

function updateDocumentTitle() {
  const unread = Object.values(unreadByGroup.value).reduce((sum, value) => sum + value, 0)
  document.title = unread > 0 ? `(${unread}) 微博助手` : '微博助手'
}

async function loadOlderMessages(triggerRemote = true) {
  if (loadingOlder.value || messages.value.length === 0) return
  olderHistoryNotice.value = ''
  if (!hasOlderMessages.value) {
    if (!triggerRemote) return
    loadingOlder.value = true
    try {
      await requestOlderHistoryPage()
    } finally {
      loadingOlder.value = false
    }
    return
  }
  const element = messageListRef.value
  const oldHeight = element?.scrollHeight || 0
  const oldest = orderedMessages.value[0]
  const requestKey = `${currentMessageRequestKey()}:${oldest?.id || ''}`
  const request = olderMessageRequests.begin(requestKey)
  loadingOlder.value = true
  try {
    const result = await listMessages({
      groupId: selectedGroupId.value || undefined,
      weiboSenderId: selectedSenderId.value || undefined,
      q: query.value.trim() || undefined,
      since: filterSince(timeRange.value),
      before: oldest?.sentAt || oldest?.capturedAt,
      limit: 50
    })
    if (!olderMessageRequests.isCurrent(request, `${currentMessageRequestKey()}:${oldest?.id || ''}`)) return
    const rawOlder = result.items || []
    const older = rawOlder.filter(isDisplayableMessage)
    const existing = new Set(messages.value.map((message) => message.id))
    messages.value.push(...older.filter((message) => !existing.has(message.id)))
    hasOlderMessages.value = rawOlder.length === 50
    await nextTick()
    if (element) element.scrollTop += element.scrollHeight - oldHeight
    if (triggerRemote && rawOlder.length < 50 && !hasActiveFilters.value) {
      await requestOlderHistoryPage()
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    if (olderMessageRequests.isCurrent(request, `${currentMessageRequestKey()}:${oldest?.id || ''}`)) {
      loadingOlder.value = false
    }
  }
}

async function requestOlderHistoryPage() {
  if (!selectedGroupId.value) {
    olderHistoryNotice.value = '请先选择群聊'
    return
  }
  if (historySyncActive.value) {
    olderHistoryNotice.value = '已有回溯任务'
    return
  }
  if (hasActiveFilters.value) {
    olderHistoryNotice.value = '清空筛选后回溯'
    return
  }
  const oldest = orderedMessages.value[0]
  const boundary = oldest?.weiboMessageId || String(oldest?.id || '')
  if (!boundary) {
    olderHistoryNotice.value = '暂无可回溯边界'
    return
  }
  if (boundary === lastAutoHistoryBoundary) {
    olderHistoryNotice.value = '已请求回溯'
    return
  }
  lastAutoHistoryBoundary = boundary
  try {
    historyJob.value = await createHistorySync(selectedGroupId.value, { pages: 1 })
    olderHistoryNotice.value = '已请求回溯'
  } catch (err) {
    lastAutoHistoryBoundary = ''
    error.value = err instanceof Error ? err.message : String(err)
  }
}

function openHistoryDialog() {
  const defaultFrom = new Date()
  defaultFrom.setDate(defaultFrom.getDate() - 30)
  historyFrom.value = toLocalDateTimeInput(defaultFrom)
  historyTo.value = ''
  historyError.value = ''
  historyDialogOpen.value = true
}

async function submitHistorySync() {
  if (!selectedGroupId.value || !historyFrom.value) return
  historySubmitting.value = true
  historyError.value = ''
  try {
    const from = new Date(historyFrom.value)
    const to = historyTo.value ? new Date(historyTo.value) : undefined
    if (Number.isNaN(from.getTime()) || (to && Number.isNaN(to.getTime()))) throw new Error('请选择有效的时间。')
    if (to && from > to) throw new Error('最早消息时间不能晚于最晚消息时间。')
    historyJob.value = await createHistorySync(selectedGroupId.value, {
      from: from.toISOString(),
      to: to?.toISOString(),
      pages: 1000
    })
    historyDialogOpen.value = false
  } catch (err) {
    historyError.value = err instanceof Error ? err.message : String(err)
  } finally {
    historySubmitting.value = false
  }
}

function toLocalDateTimeInput(value: Date) {
  const local = new Date(value.getTime() - value.getTimezoneOffset() * 60_000)
  return local.toISOString().slice(0, 16)
}

function startRuleFromMessage(message: Message) {
  editingRuleId.value = null
  ruleForm.value = {
    name: `${message.senderName || '目标用户'}提醒`,
    groupId: message.weiboGroupId || '',
    senderId: message.weiboSenderId || '',
    messageType: message.messageType || '',
    enabled: true
  }
  panelTab.value = 'rules'
  showNotifications.value = true
  ruleDialogOpen.value = true
  selectedMessage.value = null
}

function openRuleDialog() {
  editingRuleId.value = null
  panelError.value = ''
  ruleForm.value = {
    name: '',
    groupId: selectedGroup.value?.weiboGroupId || '',
    senderId: '',
    messageType: '',
    enabled: true
  }
  ruleDialogOpen.value = true
}

function closeRuleDialog() {
  ruleDialogOpen.value = false
  panelError.value = ''
}

async function copyMessageContent(message: Message) {
  await window.navigator.clipboard.writeText(message.content || '')
}

async function openMessageDetails(message: Message) {
  const requestKey = String(message.id)
  const request = detailRequests.begin(requestKey)
  selectedMessage.value = message
  try {
    const detail = await getMessage(message.id, true)
    if (detailRequests.isCurrent(request, String(selectedMessage.value?.id || ''))) {
      selectedMessage.value = detail
    }
  } catch {
    // 列表数据足以展示基础详情；原始数据加载失败不阻止详情面板打开。
  }
}

async function filterByMessageSender(message: Message) {
  selectedSenderId.value = message.weiboSenderId || ''
  selectedMessage.value = null
  await applyFilters()
}

function editRule(rule: NotificationRule) {
  editingRuleId.value = rule.id
  ruleForm.value = {
    name: rule.name,
    groupId: rule.weiboGroupId || '',
    senderId: rule.weiboSenderId || '',
    messageType: rule.messageType || '',
    enabled: rule.enabled
  }
  panelError.value = ''
  ruleDialogOpen.value = true
}

async function saveRule() {
  panelError.value = ''
  if (!ruleForm.value.groupId && !ruleForm.value.senderId && !ruleForm.value.messageType) {
    panelError.value = '请至少选择群聊、发送人或消息类型中的一个条件。'
    return
  }
  const payload = {
    name: ruleForm.value.name,
    weiboGroupId: ruleForm.value.groupId,
    weiboSenderId: ruleForm.value.senderId,
    messageType: ruleForm.value.messageType,
    enabled: ruleForm.value.enabled
  }
  try {
    if (editingRuleId.value) await updateNotificationRule(editingRuleId.value, payload)
    else await createNotificationRule(payload)
    editingRuleId.value = null
    ruleForm.value = { name: '', groupId: '', senderId: '', messageType: '', enabled: true }
    notificationRules.value = (await listNotificationRules()).items || []
    closeRuleDialog()
  } catch (err) {
    panelError.value = err instanceof Error ? err.message : String(err)
  }
}

async function removeRule(rule: NotificationRule) {
  if (rule.name === '环境变量默认通知规则') return
  await deleteNotificationRule(rule.id)
  notificationRules.value = notificationRules.value.filter((item) => item.id !== rule.id)
}

async function retryJob(item: NotificationOutboxItem) {
  await retryNotificationOutbox(item.id)
  outboxItems.value = (await listNotificationOutbox()).items || []
}

async function retryAllFailedJobs() {
  for (const item of outboxItems.value.filter((job) => job.status === 'failed')) {
    await retryNotificationOutbox(item.id)
  }
  outboxItems.value = (await listNotificationOutbox()).items || []
}

async function exportMessages(request: MessageExportRequest) {
  if (!selectedGroupId.value || exportingMessages.value) return
  exportingMessages.value = true
  exportError.value = ''
  try {
    const result = await downloadGroupMessages(selectedGroupId.value, {
      since: request.since,
      before: request.before,
      format: request.format
    })
    const url = URL.createObjectURL(result.blob)
    const link = document.createElement('a')
    link.href = url
    link.download = result.filename || request.filename
    document.body.append(link)
    link.click()
    link.remove()
    window.setTimeout(() => URL.revokeObjectURL(url), 0)
  } catch (err) {
    exportError.value = err instanceof Error ? err.message : String(err)
  } finally {
    exportingMessages.value = false
  }
}

function startSidebarResize(event: PointerEvent) {
  const startX = event.clientX
  const startWidth = sidebarWidth.value
  const move = (moveEvent: PointerEvent) => {
    sidebarWidth.value = Math.min(420, Math.max(240, startWidth + moveEvent.clientX - startX))
  }
  const stop = () => {
    window.removeEventListener('pointermove', move)
    window.removeEventListener('pointerup', stop)
  }
  window.addEventListener('pointermove', move)
  window.addEventListener('pointerup', stop)
}

function openMobileSidebar() {
  mobileSidebarOpen.value = true
}

function toggleManagementCenter() {
  showNotifications.value = !showNotifications.value
}

watch(query, () => {
  window.clearTimeout(keywordSearchTimer)
  keywordSearchTimer = window.setTimeout(() => {
    void applyFilters()
  }, 300)
})

onMounted(() => {
  clockTimer = window.setInterval(() => {
    const previousDay = currentTime.value.toDateString()
    const now = new Date()
    currentTime.value = now
    if (now.toDateString() !== previousDay) {
      void refreshDailyMessageStats().catch((err) => console.warn('refresh daily message stats after midnight failed', err))
    }
  }, 1000)
  refreshAll()
  connectRealtime()
})

onUnmounted(() => {
  messageRequests.invalidate()
  olderMessageRequests.invalidate()
  historyRequests.invalidate()
  detailRequests.invalidate()
  if (clockTimer) window.clearInterval(clockTimer)
  window.clearTimeout(keywordSearchTimer)
})
</script>

<template>
  <a-config-provider :theme="antTheme">
    <main
      class="app-shell"
      :class="{ 'with-notify': showNotifications, 'mobile-sidebar-open': mobileSidebarOpen }"
      :style="{ '--sidebar-width': `${sidebarWidth}px` }"
    >
    <div v-if="mobileSidebarOpen" class="mobile-sidebar-backdrop" @click="mobileSidebarOpen = false"></div>
    <GroupSidebar
      v-model:group-query="groupQuery"
      v-model:monitor-expanded="monitorExpanded"
      :groups="groups"
      :visible-groups="visibleGroups"
      :selected-group-id="selectedGroupId"
      :loading="loading"
      :monitor="monitor"
      :monitor-tone="monitorTone"
      :monitor-label="monitorLabel"
      :monitor-heartbeat-label="monitorHeartbeatLabel"
      :realtime-status="realtimeStatus"
      :unread-by-group="unreadByGroup"
      @refresh="refreshAll"
      @select="selectGroup"
      @resize="startSidebarResize"
    />

    <section class="chat-pane">
      <header class="chat-head">
        <div>
          <a-button class="mobile-menu-button" aria-label="打开群聊列表" @click="openMobileSidebar"><MenuOutlined /></a-button>
          <h2>{{ selectedGroup?.name || '消息流' }}</h2>
        </div>
        <div class="head-actions">
          <a-button
            :loading="historySyncProgressing"
            :disabled="!selectedGroup || historySyncActive"
            @click="openHistoryDialog"
          ><HistoryOutlined />{{ historySyncActive ? (historySyncProgressing ? '回溯中' : '回溯暂停') : '消息回溯' }}</a-button>
          <a-badge :count="actionableNotificationCount" :overflow-count="99">
            <a-button @click="toggleManagementCenter"><NotificationOutlined />管理中心</a-button>
          </a-badge>
        </div>
      </header>

      <div v-if="realtimeBanner" class="connection-banner" :class="realtimeBanner.tone" role="status">
        <span class="connection-indicator"></span>
        <span>{{ realtimeBanner.text }}</span>
      </div>

      <MessageFilters
        v-model:query="query"
        v-model:sender="senderSelectValue"
        v-model:time-range="timeRange"
        v-model:custom-date="customDate"
        :sender-options="senderOptions"
        :time-range-options="timeRangeOptions"
        :active-filters="activeFilterLabels"
        :loading="messagesLoading"
        :has-active-filters="hasActiveFilters"
        :can-view-target-sender="Boolean(appConfig.targetSenderId)"
        @apply="applyFilters"
        @sender-change="applySenderFilter"
        @view-target-sender="viewTargetSender"
        @clear="clearFilters"
        @clear-filter="clearFilter"
      />

      <div v-if="monitor && (!monitorIsFresh || monitor.lastError)" class="monitor-alert" role="alert">
        <strong>{{ monitorIsFresh ? '监控报告了异常' : '监控心跳已过期' }}</strong>
        <span>{{ monitorAlertMessage }}</span>
      </div>

      <div v-if="error" class="error-line" role="alert">
        <div>
          <strong>数据加载失败</strong>
          <span>{{ error }}</span>
        </div>
        <button type="button" @click="refreshAll">重试</button>
      </div>

      <div ref="messageListRef" class="message-list" @scroll.passive="updateAutoFollow">
        <div class="message-content">
          <div v-if="selectedGroup && !messagesLoading" class="timeline-mark">
            {{ selectedGroup.name }} · {{ hasActiveFilters ? `找到 ${orderedMessages.length} 条` : `最近 ${orderedMessages.length} 条` }}
          </div>

          <button v-if="orderedMessages.length" class="load-older" type="button" :disabled="loadingOlder || historySyncActive" @click="loadOlderMessages()">
            {{ loadOlderLabel }}
          </button>

          <div v-if="messagesLoading && orderedMessages.length === 0" class="message-skeletons" aria-label="正在加载消息">
            <div v-for="index in 5" :key="index" class="message-skeleton" :class="{ short: index % 2 === 0 }">
              <a-skeleton-avatar active class="skeleton-avatar" /><div><a-skeleton-input active class="skeleton-meta" /><a-skeleton-input active class="skeleton-bubble" /></div>
            </div>
          </div>

          <MessageItem
            v-for="message in orderedMessages"
            :key="message.id"
            :message="message"
            :query="query"
            :can-resolve-media="monitorIsFresh"
            :is-own="Boolean(appConfig.currentUserId) && message.weiboSenderId === appConfig.currentUserId"
            @details="openMessageDetails"
            @preview="previewImageURL = $event"
            @media-loaded="handleMessageMediaLoaded"
            @resolve-media="requestMessageMediaResolution"
          />

          <div v-if="!messagesLoading && orderedMessages.length === 0" class="empty-state">
            <span class="empty-icon">⌕</span>
            <strong>{{ emptyMessage }}</strong>
            <span v-if="hasActiveFilters">可以调整条件，或清空筛选查看全部消息。</span>
            <span v-else-if="selectedGroup">新消息到达后会自动出现在这里。</span>
            <button v-if="hasActiveFilters" type="button" @click="clearFilters">清空筛选</button>
          </div>
        </div>
      </div>

      <button
        v-if="!autoFollowMessages && orderedMessages.length > 0"
        class="jump-bottom"
        type="button"
        @click="scrollMessagesToBottom"
      >
        <span>{{ newMessageCount > 0 ? `${newMessageCount} 条新消息` : '回到底部' }}</span>
        <ArrowDownOutlined class="jump-bottom-icon" aria-hidden="true" />
      </button>
    </section>

    <HistorySyncDialog
      v-if="historyDialogOpen"
      v-model:from="historyFrom"
      v-model:to="historyTo"
      :submitting="historySubmitting"
      :error="historyError"
      @close="historyDialogOpen = false"
      @submit="submitHistorySync"
    />

    <ManagementCenter
      v-if="showNotifications"
      v-model:tab="panelTab"
      :outbox-items="outboxItems"
      :notification-rules="notificationRules"
      :actionable-count="actionableNotificationCount"
      :daily-message-stats="dailyMessageStats"
      :selected-group="selectedGroup"
      :history-job="historyJob"
      :history-sync-active="historySyncActive"
      :history-sync-progressing="historySyncProgressing"
      :exporting="exportingMessages"
      :export-error="exportError"
      @close="showNotifications = false"
      @retry-all="retryAllFailedJobs"
      @retry="retryJob"
      @open-rule="openRuleDialog"
      @edit-rule="editRule"
      @remove-rule="removeRule"
      @export="exportMessages"
      @open-history="openHistoryDialog"
    />

    <RuleDialog
      v-if="ruleDialogOpen"
      v-model:rule-form="ruleForm"
      :groups="groups"
      :users="users"
      :editing="Boolean(editingRuleId)"
      :error="panelError"
      @close="closeRuleDialog"
      @submit="saveRule"
    />

    <MessageDetail
      v-if="selectedMessage"
      :message="selectedMessage"
      :events="selectedMessageNotifications"
      @close="selectedMessage = null"
      @copy="copyMessageContent(selectedMessage)"
      @filter-sender="filterByMessageSender(selectedMessage)"
      @create-rule="startRuleFromMessage(selectedMessage)"
    />

    <div v-if="previewImageURL" class="image-preview" role="dialog" aria-modal="true" aria-label="图片预览" @click="previewImageURL = ''">
      <button type="button" aria-label="关闭图片预览" @click="previewImageURL = ''">×</button>
      <img :src="previewImageURL" alt="微博图片预览" referrerpolicy="no-referrer" @click.stop />
    </div>
    </main>
  </a-config-provider>
</template>
