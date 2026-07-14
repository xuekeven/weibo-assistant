<script setup lang="ts">
import type { Message, NotificationEvent } from '../api'
import { formatTime, notificationLabel } from '../utils/presentation'

defineProps<{ message: Message; events: NotificationEvent[] }>()
defineEmits<{ close: []; copy: []; filterSender: []; createRule: [] }>()
</script>

<template>
  <aside class="message-detail-pane">
    <div class="pane-head"><div><h2>消息详情</h2><p>消息 #{{ message.id }}</p></div><button class="icon-button" type="button" aria-label="关闭消息详情" @click="$emit('close')">×</button></div>
    <div class="detail-content">
      <dl><dt>发送人</dt><dd>{{ message.senderName }}</dd><dt>微博用户 ID</dt><dd>{{ message.weiboSenderId || '--' }}</dd><dt>群聊</dt><dd>{{ message.groupName }}</dd><dt>微博群 ID</dt><dd>{{ message.weiboGroupId || '--' }}</dd><dt>发送时间</dt><dd>{{ formatTime(message.sentAt) }}</dd><dt>采集时间</dt><dd>{{ formatTime(message.capturedAt) }}</dd><dt>消息类型</dt><dd>{{ message.messageType }}</dd><dt>消息 ID</dt><dd>{{ message.weiboMessageId || '--' }}</dd><dt>来源</dt><dd>{{ message.source }}</dd></dl>
      <h3>正文</h3><pre>{{ message.content || `[${message.messageType}]` }}</pre>
      <h3>通知投递记录</h3>
      <div v-if="events.length" class="detail-events"><p v-for="event in events" :key="event.id"><strong>{{ notificationLabel(event.status) }}</strong><span>{{ formatTime(event.createdAt) }}</span><small v-if="event.errorMessage">{{ event.errorMessage }}</small></p></div>
      <p v-else class="muted-text">这条消息没有通知投递记录。</p>
      <details v-if="message.raw"><summary>原始 JSON</summary><pre>{{ JSON.stringify(message.raw, null, 2) }}</pre></details>
      <div class="detail-actions"><button type="button" @click="$emit('copy')">复制正文</button><button type="button" @click="$emit('filterSender')">只看此人</button><button type="button" @click="$emit('createRule')">创建通知规则</button></div>
    </div>
  </aside>
</template>
