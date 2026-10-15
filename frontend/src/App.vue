<script setup lang="ts">
import { ArrowDownOutlined, ExportOutlined, HistoryOutlined, MenuOutlined, NotificationOutlined } from '@ant-design/icons-vue'
import { message as antMessage } from 'ant-design-vue'
import zhCN from 'ant-design-vue/es/locale/zh_CN'
import { computed, defineAsyncComponent, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import GroupSidebar from './components/GroupSidebar.vue'
import MessageFilters from './components/MessageFilters.vue'
import MessageItem from './components/MessageItem.vue'
import type { ManagementTab, MessageExportRequest, RuleForm } from './components/ManagementCenter.vue'
import { useRealtimeConnection } from './composables/useRealtimeConnection'
import { formatTime } from './utils/presentation'
import { shouldAdvanceGroupPreview } from './utils/groupPreview'
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
  refreshMonitorLoginQRCode,
  listGroups,
  listMessages,
  listNotificationEvents,
  listNotificationOutbox,
  listNotificationRules,
  listUsers,
  retryNotificationOutbox,
  retryMessageMedia,
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
const appConfig = ref({ targetSenderId: '', currentUserId: '', weiboChatUrl: '' })
const dailyMessageStats = ref<DailyMessageStats>({ today: 0, yesterday: 0 })
const users = ref<User[]>([])
const monitor = ref<MonitorStatus | null>(null)
const selectedGroupId = ref<number | null>(null)
const groupQuery = ref('')
const query = ref('')
const selectedSenderIds = ref<string[]>([])
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
const loginQRCodeURL = ref('')
const loginQRCodeOpen = ref(false)
const loginQRCodeLoading = ref(false)
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
const mediaResolutionRequests = ref(new Set<number>())
const locatingMessageId = ref<number | null>(null)
const locatedMessageId = ref<number | null>(null)
const locatedContextMessageId = ref<number | null>(null)
const messageLocationActive = computed(() => locatingMessageId.value !== null || locatedMessageId.value !== null)
const locationLoadingText = ref('')
const messageRequests = createLatestRequestGate()
const olderMessageRequests = createLatestRequestGate()
const historyRequests = createLatestRequestGate()
const detailRequests = createLatestRequestGate()
const locateMessageRequests = createLatestRequestGate()
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
  const options = users.value
    .filter((user) => user.weiboUserId)
    .map((user) => ({ label: user.nickname || '未命名用户', value: user.weiboUserId || '' }))
  if (targetSenderId && !options.some((option) => option.value === targetSenderId)) {
    options.push({ label: `关注用户（${targetSenderId}）`, value: targetSenderId })
  }
  return options
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
    key: 'message_connection_disconnected',
    label: '微博消息连接已断开',
    description: '聊天页面仍可访问，但实时消息连接已断开；重连后会自动回溯。'
  },
  {
    key: 'message_connection_unconfirmed',
    label: '微博消息连接待确认',
    description: '聊天页面正常，但尚未观察到消息连接的握手或消息帧。'
  },
  {
    key: 'browser_started',
    label: '浏览器已启动',
    description: '无头 Chrome 已打开，正在进入微博聊天页。'
  },
  {
    key: 'login_required',
    label: '等待微博扫码登录',
    description: '可点击异常提示中的“登录”刷新二维码；扫码成功后会自动恢复监听。'
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
let locateHighlightTimer: number | undefined
let suppressKeywordSearch = false
const locatePageSize = 200
const locateContextSize = 20
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
  if (status === 'login_required' || status === 'message_connection_disconnected') return 'bad'
  if (status === 'message_connection_unconfirmed') return 'warn'
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

async function openLoginQRCode() {
  if (loginQRCodeLoading.value) return
  loginQRCodeLoading.value = true
  try {
    const image = await refreshMonitorLoginQRCode()
    if (loginQRCodeURL.value) URL.revokeObjectURL(loginQRCodeURL.value)
    loginQRCodeURL.value = URL.createObjectURL(image)
    loginQRCodeOpen.value = true
  } catch (err) {
    antMessage.error(err instanceof Error ? err.message : String(err))
  } finally {
    loginQRCodeLoading.value = false
  }
}

function releaseLoginQRCode() {
  if (!loginQRCodeURL.value) return
  URL.revokeObjectURL(loginQRCodeURL.value)
  loginQRCodeURL.value = ''
}

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
  return Boolean(query.value.trim() || selectedSenderIds.value.length || timeRange.value !== 'all')
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
  if (selectedSenderIds.value.length) {
    const names = selectedSenderIds.value.map((senderID) => {
      return users.value.find((item) => item.weiboUserId === senderID)?.nickname || senderID
    })
    const senderLabel = names.length > 3 ? `${names.slice(0, 2).join('、')} 等 ${names.length} 人` : names.join('、')
    labels.push({ key: 'sender', label: `发送人：${senderLabel}` })
  }
  if (timeRange.value !== 'all') {
    const timeLabel = timeRange.value === 'custom' && customDate.value
      ? `指定日期以后：${customDate.value}`
      : timeRangeOptions.find((item) => item.value === timeRange.value)?.label || timeRange.value
    labels.push({ key: 'time', label: timeLabel })
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
    currentUserId: configResult.currentUserId || '',
    weiboChatUrl: configResult.weiboChatUrl || ''
  }

  if (!selectedGroupId.value && visibleGroups.value.length > 0) {
    selectedGroupId.value = visibleGroups.value[0].id
  }
  if (selectedGroupId.value) {
    if (locatingMessageId.value !== null) {
      await refreshHistorySync()
    } else {
      await Promise.all([refreshMessages(), refreshHistorySync()])
    }
  } else {
    messages.value = []
  }
  if (autoFollowMessages.value) {
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
    const keyword = query.value.trim() || undefined
    const since = filterSince(timeRange.value)
    const result = await listMessages({
      groupId: groupID,
      weiboSenderIds: selectedSenderIds.value,
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
    const refreshedMessages = [...merged.values()]
      .sort((a, b) => new Date(b.sentAt || b.capturedAt).getTime() - new Date(a.sentAt || a.capturedAt).getTime())
    messages.value = refreshedMessages
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
    senderIDs: selectedSenderIds.value,
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

function cancelMessageLocation() {
  locateMessageRequests.invalidate()
  locatingMessageId.value = null
  locatedMessageId.value = null
  locatedContextMessageId.value = null
  locationLoadingText.value = ''
  window.clearTimeout(locateHighlightTimer)
  locateHighlightTimer = undefined
}

async function applyFilters() {
  cancelMessageLocation()
  error.value = ''
  try {
    await refreshMessages()
    scrollMessagesToBottom(true)
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
  selectedSenderIds.value = [targetSenderId]
  await applyFilters()
}

async function clearFilters() {
  query.value = ''
  selectedSenderIds.value = []
  timeRange.value = 'all'
  customDate.value = ''
  await applyFilters()
}

async function clearFilter(key: string) {
  if (key === 'query') query.value = ''
  if (key === 'sender') selectedSenderIds.value = []
  if (key === 'time') {
    timeRange.value = 'all'
    customDate.value = ''
  }
  await applyFilters()
}

async function selectGroup(group: Group) {
  selectedGroupId.value = group.id
  messages.value = []
  cancelMessageLocation()
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
    scrollMessagesToBottom(true)
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  }
}

function messageMatchesCurrentFilters(message: Message) {
  if (selectedGroupId.value && String(message.groupId) !== String(selectedGroupId.value)) return false
  const senderIDs = selectedSenderIds.value
  if (senderIDs.length && (message.messageType === 'system' || message.messageType === 'event' || !senderIDs.includes(message.weiboSenderId || ''))) return false
  const since = filterSince(timeRange.value)
  if (since && new Date(message.sentAt || message.capturedAt).getTime() < new Date(since).getTime()) return false
  const keyword = query.value.trim().toLowerCase()
  if (keyword) {
    const haystack = (message.content || '').toLowerCase()
    if (!haystack.includes(keyword)) return false
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

function upsertMessage(message: Message, created: boolean) {
  const index = messages.value.findIndex((item) => item.id === message.id)
  if (!isDisplayableMessage(message)) {
    if (index >= 0) messages.value.splice(index, 1)
    return
  }
  const shouldFollow = created && (autoFollowMessages.value || messages.value.length === 0)
  if (created) updateGroupFromMessage(message)
  if (created && String(message.groupId) !== String(selectedGroupId.value)) {
    unreadByGroup.value[message.groupId] = (unreadByGroup.value[message.groupId] || 0) + 1
    updateDocumentTitle()
  } else if (created && !shouldFollow) {
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
    const refreshedMessages = [...messages.value]
      .sort((a, b) => new Date(b.sentAt || b.capturedAt).getTime() - new Date(a.sentAt || a.capturedAt).getTime())
    messages.value = refreshedMessages
  }
  if (shouldFollow) {
    scrollMessagesToBottom()
  }
}

function messageNeedsInitialMediaResolution(message: Message) {
  return (message.attachments || []).some((attachment) =>
    attachment.attachmentType === 'image' &&
    !(attachment.url || '').trim() &&
    !attachment.mediaResolution &&
    Array.isArray(attachment.meta?.fids) &&
    attachment.meta.fids.length > 0
  )
}

async function requestMessageMediaResolution(message: Message) {
  if (!monitorIsFresh.value || !messageNeedsInitialMediaResolution(message) || mediaResolutionRequests.value.has(message.id)) return
  setMediaResolutionBusy(message.id, true)
  try {
    await resolveMessageMedia(message.id)
    await refreshMessageMediaState(message.id)
  } catch (err) {
    console.warn('request media resolution failed', err)
  } finally {
    setMediaResolutionBusy(message.id, false)
  }
}

async function retryFailedMessageMedia(message: Message) {
  if (!monitorIsFresh.value || mediaResolutionRequests.value.has(message.id)) return
  setMediaResolutionBusy(message.id, true)
  try {
    await retryMessageMedia(message.id)
    await refreshMessageMediaState(message.id)
  } catch (err) {
    console.warn('retry media resolution failed', err)
  } finally {
    setMediaResolutionBusy(message.id, false)
  }
}

function setMediaResolutionBusy(messageId: number, busy: boolean) {
  const next = new Set(mediaResolutionRequests.value)
  if (busy) next.add(messageId)
  else next.delete(messageId)
  mediaResolutionRequests.value = next
}

async function refreshMessageMediaState(messageId: number) {
  const message = await getMessage(messageId)
  const index = messages.value.findIndex((item) => item.id === messageId)
  if (index >= 0) messages.value.splice(index, 1, message)
}

function updateGroupFromMessage(message: Message) {
  const lastMessageAt = message.sentAt || message.capturedAt
  const index = groups.value.findIndex((group) => group.id === message.groupId)
  if (index >= 0) {
    if (!shouldAdvanceGroupPreview(groups.value[index].lastMessageAt, lastMessageAt)) return
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
    upsertMessage(event.message, event.type === 'message_created')
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

function centerMessageInList(messageID: number) {
  const list = messageListRef.value
  const target = document.getElementById(`message-${messageID}`)
  if (!list || !target) return false

  const listRect = list.getBoundingClientRect()
  const targetRect = target.getBoundingClientRect()
  const targetTop = list.scrollTop + targetRect.top - listRect.top
  const centeredTop = targetTop - (list.clientHeight - targetRect.height) / 2
  const maxScrollTop = Math.max(0, list.scrollHeight - list.clientHeight)
  list.scrollTop = Math.max(0, Math.min(maxScrollTop, centeredTop))
  return true
}
function messageIsVisibleInList(messageID: number) {
  const list = messageListRef.value
  const target = document.getElementById(`message-${messageID}`)
  if (!list || !target) return false
  const listRect = list.getBoundingClientRect()
  const targetRect = target.getBoundingClientRect()
  return targetRect.top >= listRect.top && targetRect.bottom <= listRect.bottom
}

async function centerMessageUntilStable(messageID: number, requestIsCurrent: () => boolean) {
  let stableFrames = 0
  for (let attempt = 0; attempt < 30; attempt++) {
    await new Promise<void>((resolve) => window.requestAnimationFrame(() => resolve()))
    if (!requestIsCurrent() || !centerMessageInList(messageID)) return false
    await new Promise<void>((resolve) => window.requestAnimationFrame(() => resolve()))
    if (!requestIsCurrent()) return false
    stableFrames = messageIsVisibleInList(messageID) ? stableFrames + 1 : 0
    if (stableFrames >= 3) return true
  }
  return false
}

function updateAutoFollow() {
  if (messageLocationActive.value) {
    autoFollowMessages.value = false
    return
  }
  if ((messageListRef.value?.scrollTop || 0) < 80) void loadOlderMessages()
  autoFollowMessages.value = isNearMessageBottom()
  if (autoFollowMessages.value && selectedGroupId.value) {
    newMessageCount.value = 0
    unreadByGroup.value[selectedGroupId.value] = 0
    updateDocumentTitle()
  }
}

function scrollMessagesToBottom(force = false) {
  nextTick(() => {
    window.requestAnimationFrame(() => {
      if (messageLocationActive.value) return
      if (!force && !autoFollowMessages.value) return
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
  if (messageLocationActive.value) {
    const messageID = locatedMessageId.value
    if (messageID !== null) {
      window.requestAnimationFrame(() => {
        if (locatedMessageId.value === messageID) centerMessageInList(messageID)
      })
    }
    return
  }
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
  if (messageLocationActive.value || loadingOlder.value || messages.value.length === 0) return
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
      weiboSenderIds: selectedSenderIds.value,
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

async function locateMessageInContext(message: Message) {
  const groupID = selectedGroupId.value
  if (!groupID || String(message.groupId) !== String(groupID)) {
    antMessage.warning('目标消息不属于当前群聊')
    return
  }

  cancelMessageLocation()
  const requestKey = `${groupID}:${message.id}`
  const request = locateMessageRequests.begin(requestKey)
  const requestIsCurrent = () =>
    locateMessageRequests.isCurrent(request, requestKey) &&
    String(selectedGroupId.value) === String(groupID)

  locatingMessageId.value = message.id
  autoFollowMessages.value = false
  olderHistoryNotice.value = ''
  error.value = ''
  locationLoadingText.value = '正在加载目标消息的上下文…'

  suppressKeywordSearch = true
  window.clearTimeout(keywordSearchTimer)
  query.value = ''
  selectedSenderIds.value = []
  timeRange.value = 'all'
  customDate.value = ''
  await nextTick()
  window.clearTimeout(keywordSearchTimer)
  suppressKeywordSearch = false

  messages.value = []
  hasOlderMessages.value = true

  try {
    await refreshMessages()
    if (!requestIsCurrent()) return

    loadingOlder.value = true
    let previousBoundary = ''
    while (requestIsCurrent()) {
      const currentMessages = orderedMessages.value
      const targetIndex = currentMessages.findIndex((item) => item.id === message.id)
      if (targetIndex >= 0 && (targetIndex >= locateContextSize || !hasOlderMessages.value)) break
      if (!hasOlderMessages.value) break

      const oldest = currentMessages[0]
      const boundary = oldest?.sentAt || oldest?.capturedAt || ''
      if (!boundary || boundary === previousBoundary) break
      previousBoundary = boundary

      const result = await listMessages({
        groupId: groupID,
        before: boundary,
        limit: locatePageSize
      })
      if (!requestIsCurrent()) return

      const rawItems = result.items || []
      const merged = new Map(messages.value.map((item) => [item.id, item]))
      for (const item of rawItems.filter(isDisplayableMessage)) merged.set(item.id, item)
      messages.value = [...merged.values()]
      hasOlderMessages.value = rawItems.length === locatePageSize
    }

    if (!requestIsCurrent()) return
    if (!messages.value.some((item) => item.id === message.id)) {
      antMessage.warning('没有在已保存的群聊记录中找到这条消息')
      return
    }

    locationLoadingText.value = '正在滚动到目标消息…'
    locatedMessageId.value = message.id
    await nextTick()
    const targetFound = await centerMessageUntilStable(message.id, requestIsCurrent)
    if (!targetFound) {
      antMessage.warning('消息已加载，但暂时无法滚动到目标位置')
      return
    }
    autoFollowMessages.value = false
    locatedContextMessageId.value = message.id
    locateHighlightTimer = window.setTimeout(() => {
      if (locatedMessageId.value === message.id) locatedMessageId.value = null
    }, 6000)
    antMessage.success('已定位到目标消息')
  } catch (err) {
    if (!requestIsCurrent()) return
    error.value = err instanceof Error ? err.message : String(err)
    antMessage.error('定位消息失败')
  } finally {
    if (requestIsCurrent()) {
      loadingOlder.value = false
      locationLoadingText.value = ''
      locatingMessageId.value = null
    }
  }
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
      format: request.format,
      weiboSenderIds: request.weiboSenderIds
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
  if (suppressKeywordSearch) return
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
  cancelMessageLocation()
  if (clockTimer) window.clearInterval(clockTimer)
  window.clearTimeout(keywordSearchTimer)
  releaseLoginQRCode()
})
</script>

<template>
  <a-config-provider :theme="antTheme" :locale="zhCN">
    <main
      class="app-shell"
      :class="{ 'with-notify': showNotifications, 'mobile-sidebar-open': mobileSidebarOpen, 'location-loading': locatingMessageId !== null }"
      :aria-busy="locatingMessageId !== null"
      :style="{ '--sidebar-width': `${sidebarWidth}px` }"
    >
      <div v-if="locatingMessageId !== null" class="global-location-loading" role="status" aria-live="polite">
        <a-spin size="large" />
        <strong>正在定位消息</strong>
        <span>{{ locationLoadingText }}</span>
      </div>
    <div v-if="mobileSidebarOpen" class="mobile-sidebar-backdrop" @click="mobileSidebarOpen = false"></div>
    <GroupSidebar
      v-model:group-query="groupQuery"
      v-model:monitor-expanded="monitorExpanded"
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
          <a
            v-if="selectedGroup && appConfig.weiboChatUrl"
            class="chat-group-link"
            :href="appConfig.weiboChatUrl"
            target="_blank"
            rel="noopener noreferrer"
            :aria-label="`在新页签打开微博网页版群聊：${selectedGroup.name}`"
          >
            <span>{{ selectedGroup.name }}</span>
            <ExportOutlined aria-hidden="true" />
          </a>
          <h2 v-else>{{ selectedGroup?.name || '消息流' }}</h2>
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
        v-model:sender="selectedSenderIds"
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
        <button
          v-if="monitorIsFresh && monitor.status === 'login_required'"
          class="monitor-login-button"
          type="button"
          :disabled="loginQRCodeLoading"
          @click="openLoginQRCode"
        >
          {{ loginQRCodeLoading ? '正在刷新…' : '登录' }}
        </button>
      </div>

      <div v-if="error" class="error-line" role="alert">
        <div>
          <strong>数据加载失败</strong>
          <span>{{ error }}</span>
        </div>
        <button type="button" @click="refreshAll">重试</button>
      </div>

      <div ref="messageListRef" class="message-list" :class="{ 'message-location-active': messageLocationActive }" @scroll.passive="updateAutoFollow">
        <div class="message-content">
          <div v-if="selectedGroup && !messagesLoading" class="timeline-mark">
            {{ selectedGroup.name }} · {{ hasActiveFilters ? `找到 ${orderedMessages.length} 条` : `最近 ${orderedMessages.length} 条` }}
          </div>

          <button v-if="orderedMessages.length" class="load-older" type="button" :disabled="messageLocationActive || loadingOlder || historySyncActive" @click="loadOlderMessages()">
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
            :media-resolution-busy="mediaResolutionRequests.has(message.id)"
            :is-own="Boolean(appConfig.currentUserId) && message.weiboSenderId === appConfig.currentUserId"
            :is-followed="Boolean(appConfig.targetSenderId.trim()) && message.weiboSenderId === appConfig.targetSenderId.trim()"
            :located="locatedMessageId === message.id"
            @details="openMessageDetails"
            @locate="locateMessageInContext"
            @create-rule="startRuleFromMessage"
            @preview="previewImageURL = $event"
            @media-loaded="handleMessageMediaLoaded"
            @resolve-media="requestMessageMediaResolution"
            @retry-media="retryFailedMessageMedia"
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
        @click="scrollMessagesToBottom(true)"
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
      :users="users"
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
    />

    <a-modal
      v-model:open="loginQRCodeOpen"
      title="微博扫码登录"
      :footer="null"
      :width="420"
      centered
      @after-close="releaseLoginQRCode"
    >
      <div class="login-qr-dialog">
        <img v-if="loginQRCodeURL" :src="loginQRCodeURL" alt="微博登录二维码" />
        <p>请使用微博 App 扫描二维码。登录成功后 monitor 会自动恢复群聊监听。</p>
      </div>
    </a-modal>

    <div v-if="previewImageURL" class="image-preview" role="dialog" aria-modal="true" aria-label="图片预览" @click="previewImageURL = ''">
      <button type="button" aria-label="关闭图片预览" @click="previewImageURL = ''">×</button>
      <img :src="previewImageURL" alt="微博图片预览" referrerpolicy="no-referrer" @click.stop />
    </div>
    </main>
  </a-config-provider>
</template>
