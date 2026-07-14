export function relativeDayLabel(date: Date, now = new Date()) {
  const targetDay = new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime()
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime()
  const dayDifference = Math.round((today - targetDay) / 86_400_000)

  if (dayDifference === 0) return '今天'
  if (dayDifference === 1) return '昨天'
  return new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit' }).format(date)
}

export function formatTime(value?: string, options: { seconds?: boolean; now?: Date } = {}) {
  if (!value) return '--'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value

  const clock = new Intl.DateTimeFormat('zh-CN', {
    hour: '2-digit',
    minute: '2-digit',
    ...(options.seconds === false ? {} : { second: '2-digit' }),
    hour12: false
  }).format(date)

  return `${relativeDayLabel(date, options.now)} ${clock}`
}

export function avatarColor(value?: string) {
  const text = value || '?'
  let hash = 0
  for (const char of text) hash = (hash * 31 + char.charCodeAt(0)) | 0
  return `hsl(${Math.abs(hash) % 360} 48% 38%)`
}

export function initials(name?: string) {
  return (name || '?').trim().slice(0, 1).toUpperCase()
}

export function notificationLabel(status: string) {
  if (status === 'sent') return '已发送'
  if (status === 'failed') return '失败'
  if (status === 'retrying' || status === 'retry') return '等待重试'
  if (status === 'pending') return '等待发送'
  if (status === 'sending') return '正在发送'
  if (status === 'skipped') return '跳过'
  return status
}
