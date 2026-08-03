<script setup lang="ts">
import { BellOutlined, CopyOutlined, FileTextOutlined, ReloadOutlined, UserOutlined } from '@ant-design/icons-vue'
import { computed, ref, watch } from 'vue'
import { avatarDisplayURL, type Attachment, type Message } from '../api'
import { avatarColor, formatTime, initials } from '../utils/presentation'

const props = defineProps<{
  message: Message
  query: string
  canResolveMedia: boolean
  mediaResolutionBusy: boolean
  isOwn: boolean
}>()

const emit = defineEmits<{
  details: [message: Message]
  copy: [message: Message]
  filterSender: [message: Message]
  createRule: [message: Message]
  preview: [url: string]
  mediaLoaded: []
  resolveMedia: [message: Message]
  retryMedia: [message: Message]
}>()

const imageAttachments = computed(() => (props.message.attachments || []).filter((attachment) => attachment.attachmentType === 'image'))
const videoAttachments = computed(() => (props.message.attachments || []).filter((attachment) => attachment.attachmentType === 'video'))
const isVideoMessage = computed(() => props.message.messageType === 'video' || videoAttachments.value.length > 0)
const parts = computed(() => messageContentParts(props.message.content))
const unqueuedImageFIDs = computed(() => imageAttachments.value
  .filter((attachment) => !(attachment.url || '').trim() && !attachment.mediaResolution)
  .flatMap(attachmentFids)
  .filter(Boolean))
const failedAvatarURL = ref('')
const failedImageURLs = ref<Record<string, boolean>>({})

function messageContentParts(content?: string) {
  const text = (content || '').trim()
  const values = text.split(/\n\s*-{5,}\s*\n/, 2)
  if (values.length < 2) return { quote: '', body: text }
  return { quote: values[0].trim(), body: values[1].trim() }
}

function contentTokens(content: string) {
  const keyword = props.query.trim()
  const pattern = keyword ? `(${escapeRegExp(keyword)}|@[\\p{L}\\p{N}_-]+)` : '(@[\\p{L}\\p{N}_-]+)'
  const regex = new RegExp(pattern, 'giu')
  return content.split(regex).filter(Boolean).map((text) => ({
    text,
    tone: keyword && text.toLowerCase() === keyword.toLowerCase() ? 'highlight' : text.startsWith('@') ? 'mention' : ''
  }))
}

function escapeRegExp(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

function standaloneURL(content?: string) {
  const text = (content || '').trim()
  if (!/^https?:\/\/[^\s<>"']+$/i.test(text)) return ''

  try {
    const url = new URL(text)
    return url.protocol === 'http:' || url.protocol === 'https:' ? url.toString() : ''
  } catch {
    return ''
  }
}

function attachmentFids(attachment: Attachment) {
  const values = attachment.meta?.fids
  return Array.isArray(values) ? values.map((fid) => String(fid)) : []
}

function imagePreviewURL(attachment: Attachment) {
  const rawURL = (attachment.url || '').trim()
  if (!rawURL) return ''

  try {
    const url = new URL(rawURL)
    if (!url.pathname.endsWith('/msget_thumbnail') && !url.pathname.endsWith('/msget')) {
      return rawURL
    }

    const fid = url.searchParams.get('fid') || attachmentFids(attachment)[0]
    if (!fid) return rawURL

    const previewURL = new URL('/2/mss/msget', url.origin)
    previewURL.searchParams.set('fid', fid)
    previewURL.searchParams.set('source', url.searchParams.get('source') || '209678993')
    previewURL.searchParams.set('imageType', url.searchParams.get('imageType') || 'origin')
    previewURL.searchParams.set('ts', String(Date.now()))
    return previewURL.toString()
  } catch {
    return rawURL
  }
}

function imageKey(attachment: Attachment) {
  return String(attachment.id || attachment.url || JSON.stringify(attachment.meta || {}))
}

function markImageFailed(attachment: Attachment) {
  failedImageURLs.value = { ...failedImageURLs.value, [imageKey(attachment)]: true }
}

function mediaResolutionLabel(attachment: Attachment) {
  switch (attachment.mediaResolution?.status) {
    case 'pending': return '图片排队解析'
    case 'running': return '图片解析中'
    case 'failed': return '图片解析失败'
    default: return '图片待解析'
  }
}

function mediaResolutionHint(attachment: Attachment) {
  const resolution = attachment.mediaResolution
  if (!resolution) return '等待 monitor 创建解析任务'
  const attempts = resolution.maxAttempts > 0 ? `已尝试 ${resolution.attemptCount}/${resolution.maxAttempts} 次` : ''
  if (resolution.status === 'failed') return `${attempts}；微博图片接口暂时无法访问`
  if (resolution.status === 'pending' && resolution.attemptCount > 0) return `${attempts}；等待再次解析`
  return attempts || mediaResolutionLabel(attachment)
}

watch(
  [() => props.canResolveMedia, () => props.message.id, () => unqueuedImageFIDs.value.join(',')],
  ([canResolve]) => {
    if (canResolve && unqueuedImageFIDs.value.length > 0) {
      emit('resolveMedia', props.message)
    }
  },
  { immediate: true }
)
</script>

<template>
  <article v-if="message.messageType === 'system'" class="message-row system-message">
    <button type="button" :title="formatTime(message.sentAt || message.capturedAt)" @click="$emit('details', message)">{{ message.content || '微博群聊系统消息' }}</button>
  </article>
  <article v-else class="message-row" :class="{ 'own-message': isOwn }">
    <img v-if="message.senderAvatar && failedAvatarURL !== message.senderAvatar" class="avatar sender" :src="avatarDisplayURL(message.senderAvatar)" alt="" loading="lazy" referrerpolicy="no-referrer" @error="failedAvatarURL = message.senderAvatar || ''" />
    <span v-else class="avatar sender" :style="{ backgroundColor: avatarColor(message.weiboSenderId || message.senderName) }">{{ initials(message.senderName) }}</span>
    <div class="message-main">
      <div class="message-meta">
        <strong :title="message.weiboSenderId ? `微博用户 ID：${message.weiboSenderId}` : undefined">{{ message.senderName || message.weiboSenderId || '未知发送人' }}</strong>
        <time :title="formatTime(message.sentAt || message.capturedAt)">{{ formatTime(message.sentAt || message.capturedAt, { seconds: false }) }}</time>
        <span class="message-actions">
          <button class="message-secondary-action" type="button" @click="$emit('details', message)"><FileTextOutlined />详情</button>
          <button class="message-secondary-action" type="button" @click="$emit('copy', message)"><CopyOutlined />复制正文</button>
          <button class="message-secondary-action" type="button" @click="$emit('filterSender', message)"><UserOutlined />只看此人</button>
          <button class="message-secondary-action" type="button" @click="$emit('createRule', message)"><BellOutlined />创建通知规则</button>
        </span>
      </div>
      <div class="bubble" :class="{ media: imageAttachments.length > 0 || isVideoMessage, recalled: message.messageType === 'recall' }">
        <template v-if="message.content && (message.content !== '分享图片' || imageAttachments.length === 0) && (message.content !== '分享视频' || !isVideoMessage)">
          <blockquote v-if="parts.quote"><span v-for="(token, index) in contentTokens(parts.quote)" :key="index" :class="token.tone">{{ token.text }}</span></blockquote>
          <p v-if="parts.body">
            <a
              v-if="standaloneURL(parts.body)"
              class="message-link"
              :href="standaloneURL(parts.body)"
              target="_blank"
              rel="noopener noreferrer"
            >{{ parts.body }}</a>
            <template v-else>
              <span v-for="(token, index) in contentTokens(parts.body)" :key="index" :class="token.tone">{{ token.text }}</span>
            </template>
          </p>
        </template>
        <div v-if="isVideoMessage" class="video-card">
          <span class="video-badge">视频消息</span>
          <small v-if="videoAttachments.length > 0">视频 fid {{ attachmentFids(videoAttachments[0]).join(', ') || '待解析' }}</small>
        </div>
        <div v-if="imageAttachments.length > 0" class="image-grid">
          <template v-for="attachment in imageAttachments" :key="attachment.id || attachment.url || JSON.stringify(attachment.meta)">
            <button v-if="attachment.url" class="image-item" type="button" title="预览图片" @click="$emit('preview', imagePreviewURL(attachment))">
              <img
                :src="attachment.url"
                alt="微博图片消息"
                loading="lazy"
                referrerpolicy="no-referrer"
                @load="$emit('mediaLoaded')"
                @error="markImageFailed(attachment)"
              />
              <span v-if="failedImageURLs[imageKey(attachment)]" class="image-fallback">图片加载失败</span>
            </button>
            <span
              v-else
              class="image-item pending"
              :class="{ failed: attachment.mediaResolution?.status === 'failed', running: attachment.mediaResolution?.status === 'running' }"
              :title="mediaResolutionHint(attachment)"
            >
              <strong>{{ mediaResolutionLabel(attachment) }}</strong>
              <small v-if="attachmentFids(attachment).length > 0">fid {{ attachmentFids(attachment).join(', ') }}</small>
              <button
                v-if="attachment.mediaResolution?.status === 'failed'"
                class="media-retry-button"
                type="button"
                aria-label="重新解析图片"
                :title="canResolveMedia ? '重新解析图片' : 'monitor 离线，暂时无法重试'"
                :disabled="!canResolveMedia || mediaResolutionBusy"
                @click="$emit('retryMedia', message)"
              ><ReloadOutlined :spin="mediaResolutionBusy" /></button>
            </span>
          </template>
        </div>
        <p v-if="!message.content && imageAttachments.length === 0">[{{ message.messageType }}]</p>
      </div>
    </div>
  </article>
</template>
