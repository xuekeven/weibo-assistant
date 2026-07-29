const defaultAPIBase = `${window.location.protocol}//${window.location.hostname}:6001`
const API_BASE = (import.meta.env.VITE_API_BASE || defaultAPIBase).replace(/\/$/, '')
const WS_BASE = API_BASE.replace(/^http:/, 'ws:').replace(/^https:/, 'wss:')

export function avatarDisplayURL(rawURL?: string) {
  const value = (rawURL || '').trim()
  if (!value) return ''
  try {
    const parsed = new URL(value)
    const host = parsed.hostname.toLowerCase().replace(/\.$/, '')
    if (parsed.protocol === 'https:' && (host === 'sinaimg.cn' || host.endsWith('.sinaimg.cn'))) {
      return `${API_BASE}/api/media/avatar?url=${encodeURIComponent(parsed.toString())}`
    }
  } catch {
    return value
  }
  return value
}

export type Group = {
  id: number
  weiboGroupId?: string
  name: string
  avatarUrl?: string
  lastMessageAt?: string
  lastMessagePreview?: string
  updatedAt: string
}

export type User = {
  id: number
  weiboUserId?: string
  nickname: string
  avatarUrl?: string
  updatedAt: string
}

export type Message = {
  id: number
  weiboMessageId?: string
  groupId: number
  weiboGroupId?: string
  groupName?: string
  senderId?: number
  weiboSenderId?: string
  senderName?: string
  senderAvatar?: string
  messageType: string
  content?: string
  sentAt?: string
  capturedAt: string
  source: string
  dedupeKey: string
  attachments?: Attachment[]
  createdAt: string
  raw?: Record<string, unknown> | null
}

export type Attachment = {
  id?: number
  messageId?: number
  attachmentType: string
  url?: string
  localPath?: string
  meta?: Record<string, unknown> | null
  createdAt?: string
}

export type MonitorStatus = {
  status: string
  currentUrl?: string
  lastMessageAt?: string
  lastHeartbeatAt?: string
  lastError?: string
  updatedAt?: string
  fresh?: boolean
  heartbeatAgeSecs?: number
  staleAfterSecs?: number
}

export type HistorySyncJob = {
  id: number
  groupId: number
  weiboGroupId: string
  groupName: string
  mode: 'startup' | 'recovery' | 'manual'
  requestedFrom?: string
  requestedTo?: string
  cursorMid?: string
  stopMid?: string
  status: 'pending' | 'running' | 'completed' | 'failed'
  maxPages: number
  pagesFetched: number
  messagesSeen: number
  messagesInserted: number
  stopReason?: string
  lastError?: string
  createdAt: string
  updatedAt: string
}

export type NotificationEvent = {
  id: number
  messageId: number
  status: string
  relayUrl?: string
  touser: string
  errorMessage?: string
  createdAt: string
}

export type NotificationRule = {
  id: number
  name: string
  weiboGroupId?: string
  groupName?: string
  weiboSenderId?: string
  senderNickname?: string
  messageType?: string
  enabled: boolean
  hitCount: number
  finalFailureCount: number
  lastMatchedAt?: string
}

export type NotificationOutboxItem = {
  id: number
  ruleId?: number
  messageId: number
  content: string
  touser?: string
  status: string
  attemptCount: number
  maxAttempts: number
  nextAttemptAt: string
  lastError?: string
  sentAt?: string
  updatedAt: string
}

export type AppConfig = {
  targetSenderId?: string
  currentUserId?: string
  weiboChatUrl?: string
}

export type DailyMessageStats = {
  today: number
  yesterday: number
}

export type MessageExportFormat = 'csv' | 'json' | 'md'

export type RealtimeEvent = {
  type: string
  message?: Message
  monitorStatus?: MonitorStatus
  notificationEvent?: NotificationEvent
  historySyncJob?: HistorySyncJob
  sentAt?: string
}

type ListResponse<T> = {
  items: T[] | null
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const controller = new AbortController()
  const timeout = window.setTimeout(() => controller.abort(), 8000)
  try {
    const response = await fetch(`${API_BASE}${path}`, {
      ...init,
      headers: init.body ? { 'Content-Type': 'application/json', ...init.headers } : init.headers,
      signal: controller.signal
    })
    if (!response.ok) {
      const text = await response.text()
      let message = text
      try {
        const parsed = JSON.parse(text) as { error?: string; requestId?: string }
        message = parsed.error || text
        if (parsed.requestId) message += `（请求 ID：${parsed.requestId}）`
      } catch {
        // 非 JSON 错误响应直接展示原始文本。
      }
      throw new Error(message || `请求失败（HTTP ${response.status}）`)
    }
    if (response.status === 204) return undefined as T
    return response.json() as Promise<T>
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') {
      throw new Error(`请求后端超时：${API_BASE}${path}`, { cause: err })
    }
    throw err
  } finally {
    window.clearTimeout(timeout)
  }
}

export function listGroups(limit = 100) {
  return request<ListResponse<Group>>(`/api/groups?limit=${limit}`)
}

export function listMessages(params: {
  groupId?: number
  weiboGroupId?: string
  weiboSenderId?: string
  messageType?: string
  q?: string
  since?: string
  before?: string
  limit?: number
}) {
  const query = new URLSearchParams()
  query.set('limit', String(params.limit || 80))
  query.set('includeRaw', 'false')
  if (params.groupId) query.set('groupId', String(params.groupId))
  if (params.weiboGroupId) query.set('weiboGroupId', params.weiboGroupId)
  if (params.weiboSenderId) query.set('weiboSenderId', params.weiboSenderId)
  if (params.messageType) query.set('messageType', params.messageType)
  if (params.q) query.set('q', params.q)
  if (params.since) query.set('since', params.since)
  if (params.before) query.set('before', params.before)
  return request<ListResponse<Message>>(`/api/messages?${query.toString()}`)
}

export function getGroupDailyMessageStats(groupId: number, range: {
  yesterdayStart: string
  todayStart: string
  tomorrowStart: string
}) {
  const query = new URLSearchParams(range)
  return request<DailyMessageStats>(`/api/groups/${groupId}/message-stats?${query.toString()}`)
}

export async function downloadGroupMessages(groupId: number, params: {
  since: string
  before: string
  format: MessageExportFormat
  weiboSenderIds?: string[]
}) {
  const query = new URLSearchParams({
    since: params.since,
    before: params.before,
    format: params.format
  })
  for (const senderID of params.weiboSenderIds || []) {
    query.append('weiboSenderId', senderID)
  }
  const controller = new AbortController()
  const timeout = window.setTimeout(() => controller.abort(), 60_000)
  try {
    const response = await fetch(`${API_BASE}/api/groups/${groupId}/messages/export?${query.toString()}`, {
      signal: controller.signal
    })
    if (!response.ok) {
      const text = await response.text()
      let message = text
      try {
        const parsed = JSON.parse(text) as { error?: string; requestId?: string }
        message = parsed.error || text
        if (parsed.requestId) message += `（请求 ID：${parsed.requestId}）`
      } catch {
        // 非 JSON 错误响应直接显示原文。
      }
      throw new Error(message || `导出失败（HTTP ${response.status}）`)
    }
    const disposition = response.headers.get('Content-Disposition') || ''
    const filename = disposition.match(/filename="([^"]+)"/)?.[1] || `weibo-messages.${params.format}`
    return { blob: await response.blob(), filename }
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') {
      throw new Error('导出请求超时', { cause: err })
    }
    throw err
  } finally {
    window.clearTimeout(timeout)
  }
}

export function getMessage(id: number, includeRaw = false) {
  return request<Message>(`/api/messages/${id}?includeRaw=${includeRaw}`)
}

export function resolveMessageMedia(id: number) {
  return request<{ messageId: number; queued: number }>(`/api/messages/${id}/resolve-media`, {
    method: 'POST'
  })
}

export function listUsers(q = '', limit = 200) {
  const query = new URLSearchParams({ limit: String(limit) })
  if (q.trim()) query.set('q', q.trim())
  return request<ListResponse<User>>(`/api/users?${query.toString()}`)
}

export function getMonitorStatus() {
  return request<MonitorStatus>('/api/monitor/status')
}

export function getAppConfig() {
  return request<AppConfig>('/api/config')
}

export function getHistorySync(groupId: number) {
  return request<{ job: HistorySyncJob | null }>(`/api/groups/${groupId}/history-sync`)
}

export function createHistorySync(groupId: number, payload: { from?: string; to?: string; pages?: number }) {
  return request<HistorySyncJob>(`/api/groups/${groupId}/history-sync`, {
    method: 'POST',
    body: JSON.stringify(payload)
  })
}

export function listNotificationEvents(limit = 30) {
  return request<ListResponse<NotificationEvent>>(`/api/notification-events?limit=${limit}`)
}

export function listNotificationRules(limit = 100) {
  return request<ListResponse<NotificationRule>>(`/api/notification-rules?limit=${limit}`)
}

export function createNotificationRule(payload: Record<string, unknown>) {
  return request<NotificationRule>('/api/notification-rules', { method: 'POST', body: JSON.stringify(payload) })
}

export function updateNotificationRule(id: number, payload: Record<string, unknown>) {
  return request<NotificationRule>(`/api/notification-rules/${id}`, { method: 'PATCH', body: JSON.stringify(payload) })
}

export function deleteNotificationRule(id: number) {
  return request<void>(`/api/notification-rules/${id}`, { method: 'DELETE' })
}

export function listNotificationOutbox(limit = 100) {
  return request<ListResponse<NotificationOutboxItem>>(`/api/notification-outbox?limit=${limit}`)
}

export function retryNotificationOutbox(id: number) {
  return request<{ status: string }>(`/api/notification-outbox/${id}/retry`, { method: 'POST' })
}

export function getReadiness() {
  return request<Record<string, unknown>>('/readyz')
}

export function createRealtimeSocket() {
  return new WebSocket(`${WS_BASE}/api/ws`)
}
