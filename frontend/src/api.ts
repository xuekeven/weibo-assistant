const defaultAPIBase = `${window.location.protocol}//${window.location.hostname}:6001`
const API_BASE = (import.meta.env.VITE_API_BASE || defaultAPIBase).replace(/\/$/, '')
const WS_BASE = API_BASE.replace(/^http:/, 'ws:').replace(/^https:/, 'wss:')

export type Group = {
  id: number
  weiboGroupId?: string
  name: string
  avatarUrl?: string
  lastMessageAt?: string
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
  messageType: string
  content?: string
  sentAt?: string
  capturedAt: string
  source: string
  dedupeKey: string
  attachments?: Attachment[]
  createdAt: string
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

export type NotificationEvent = {
  id: number
  messageId: number
  status: string
  relayUrl?: string
  touser?: string
  errorMessage?: string
  createdAt: string
}

export type RealtimeEvent = {
  type: string
  message?: Message
  monitorStatus?: MonitorStatus
  notificationEvent?: NotificationEvent
  sentAt?: string
}

type ListResponse<T> = {
  items: T[] | null
}

async function request<T>(path: string): Promise<T> {
  const controller = new AbortController()
  const timeout = window.setTimeout(() => controller.abort(), 8000)
  try {
    const response = await fetch(`${API_BASE}${path}`, {
      signal: controller.signal
    })
    if (!response.ok) {
      const text = await response.text()
      throw new Error(text || `HTTP ${response.status}`)
    }
    return response.json() as Promise<T>
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') {
      throw new Error(`请求后端超时：${API_BASE}${path}`)
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
  q?: string
  limit?: number
}) {
  const query = new URLSearchParams()
  query.set('limit', String(params.limit || 80))
  query.set('includeRaw', 'false')
  if (params.groupId) query.set('groupId', String(params.groupId))
  if (params.weiboGroupId) query.set('weiboGroupId', params.weiboGroupId)
  if (params.weiboSenderId) query.set('weiboSenderId', params.weiboSenderId)
  if (params.q) query.set('q', params.q)
  return request<ListResponse<Message>>(`/api/messages?${query.toString()}`)
}

export function getMonitorStatus() {
  return request<MonitorStatus>('/api/monitor/status')
}

export function listNotificationEvents(limit = 30) {
  return request<ListResponse<NotificationEvent>>(`/api/notification-events?limit=${limit}`)
}

export function createRealtimeSocket() {
  return new WebSocket(`${WS_BASE}/api/ws`)
}
