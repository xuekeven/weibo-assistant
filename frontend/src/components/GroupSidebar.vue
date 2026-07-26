<script setup lang="ts">
import { DownOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons-vue'
import type { Group, MonitorStatus } from '../api'
import { avatarColor, formatTime, initials } from '../utils/presentation'

defineProps<{
  groups: Group[]
  visibleGroups: Group[]
  selectedGroupId: number | null
  loading: boolean
  monitor: MonitorStatus | null
  monitorTone: string
  monitorLabel: string
  monitorHeartbeatLabel: string
  realtimeStatus: string
  unreadByGroup: Record<number, number>
}>()

const groupQuery = defineModel<string>('groupQuery', { required: true })
const monitorExpanded = defineModel<boolean>('monitorExpanded', { required: true })

defineEmits<{
  refresh: []
  select: [group: Group]
  resize: [event: PointerEvent]
}>()
</script>

<template>
  <aside class="group-pane" aria-label="群聊列表">
    <div class="pane-head">
      <div>
        <h1>微博助手</h1>
        <p>{{ groupQuery ? `${visibleGroups.length}/${groups.length}` : visibleGroups.length }} 个群聊</p>
      </div>
      <div class="pane-actions">
        <a-button size="small" aria-label="刷新全部数据" :aria-busy="loading" :disabled="loading" @click="$emit('refresh')"><ReloadOutlined :spin="loading" /></a-button>
      </div>
    </div>

    <button class="monitor-strip" :class="monitorTone" type="button" :aria-expanded="monitorExpanded" @click="monitorExpanded = !monitorExpanded">
      <span class="monitor-copy">
        <span class="monitor-title-row">
          <span class="status-dot"></span>
          <strong>{{ monitorLabel }}</strong>
          <DownOutlined class="monitor-chevron" :class="{ expanded: monitorExpanded }" />
        </span>
        <span v-if="monitorExpanded" class="monitor-details">
          <span><b>Chrome / CDP</b><em>{{ monitorLabel }}</em></span>
          <span><b>前端实时连接</b><em>{{ realtimeStatus === 'connected' ? '已连接' : '正在恢复' }}</em></span>
          <span><b>最近心跳</b><em>{{ monitorHeartbeatLabel.replace('最后监控：', '') }}</em></span>
          <span><b>最近消息</b><em>{{ formatTime(monitor?.lastMessageAt) }}</em></span>
          <span v-if="monitor?.lastError" class="detail-error"><b>最近错误</b><em>{{ monitor.lastError }}</em></span>
        </span>
      </span>
    </button>

    <div class="group-search">
      <a-input v-model:value="groupQuery" size="large" placeholder="搜索群聊" aria-label="搜索群聊" class="group-search-input"><template #prefix><SearchOutlined /></template></a-input>
    </div>

    <div class="group-list">
      <div v-if="loading && visibleGroups.length === 0" class="group-skeletons" aria-label="正在加载群聊">
        <a-skeleton v-for="index in 3" :key="index" active :title="false" :paragraph="{ rows: 2 }" class="group-skeleton" />
      </div>
      <button v-for="group in visibleGroups" :key="group.id" class="group-row" :class="{ active: String(group.id) === String(selectedGroupId) }" type="button" @click="$emit('select', group)">
        <img v-if="group.avatarUrl" class="avatar" :src="group.avatarUrl" alt="" referrerpolicy="no-referrer" />
        <span v-else class="avatar" :style="{ backgroundColor: avatarColor(group.weiboGroupId || group.name) }">{{ initials(group.name) }}</span>
        <span class="group-copy"><strong>{{ group.name }}</strong><span>{{ group.lastMessagePreview || '等待新消息' }}</span></span>
        <time>{{ formatTime(group.lastMessageAt || group.updatedAt, { seconds: false }) }}</time>
        <a-badge v-if="unreadByGroup[group.id]" class="unread-badge" :count="unreadByGroup[group.id]" :overflow-count="99" />
      </button>
      <div v-if="!loading && visibleGroups.length === 0" class="sidebar-empty">
        <strong>{{ groupQuery ? '没有匹配的群聊' : '还没有群聊' }}</strong>
        <span>{{ groupQuery ? '尝试更换关键词。' : 'monitor 采集到消息后会自动显示在这里。' }}</span>
        <button v-if="groupQuery" type="button" @click="groupQuery = ''">清空搜索</button>
        <button v-else type="button" @click="$emit('refresh')">重新加载</button>
      </div>
    </div>
    <button class="sidebar-resizer" type="button" aria-label="拖动调整群聊栏宽度" @pointerdown="$emit('resize', $event)"></button>
  </aside>
</template>
