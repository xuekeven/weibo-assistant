<script setup lang="ts">
import { SearchOutlined } from '@ant-design/icons-vue'

type Option = { label: string; value: string }
type FilterLabel = { key: string; label: string }

defineProps<{
  senderOptions: Option[]
  timeRangeOptions: Option[]
  activeFilters: FilterLabel[]
  loading: boolean
  hasActiveFilters: boolean
  canViewTargetSender: boolean
}>()

const query = defineModel<string>('query', { required: true })
const sender = defineModel<string>('sender', { required: true })
const timeRange = defineModel<string>('timeRange', { required: true })
const customDate = defineModel<string>('customDate', { required: true })

defineEmits<{
  apply: []
  clear: []
  clearFilter: [key: string]
  senderChange: []
  viewTargetSender: []
}>()
</script>

<template>
  <form class="filters" :class="{ 'has-custom-date': timeRange === 'custom' }" @submit.prevent="$emit('apply')">
    <label class="filter-field keyword-field"><span>关键词</span><a-input v-model:value="query" size="large" placeholder="模糊搜索消息正文" class="message-search-input"><template #prefix><SearchOutlined /></template></a-input></label>
    <label class="filter-field sender-field">
      <span>发送人</span>
      <a-select v-model:value="sender" size="large" show-search :options="senderOptions" option-filter-prop="label" placeholder="搜索发送人" class="sender-input-menu" not-found-content="没有匹配的发送人" @change="$emit('senderChange')" />
    </label>
    <label class="filter-field compact-field"><span>时间</span><a-select v-model:value="timeRange" size="large" :options="timeRangeOptions" /></label>
    <label v-if="timeRange === 'custom'" class="filter-field compact-field custom-date-field"><span>开始日期</span><a-input v-model:value="customDate" size="large" type="date" /></label>
    <div class="filter-actions">
      <a-button class="filter-submit" type="primary" size="large" :loading="loading" :disabled="!canViewTargetSender" @click="$emit('viewTargetSender')">查看关注</a-button>
      <a-button v-if="hasActiveFilters" class="filter-reset" size="large" :disabled="loading" @click="$emit('clear')">清空</a-button>
    </div>
    <div v-if="activeFilters.length" class="active-filters" aria-label="当前筛选条件">
      <button v-for="filter in activeFilters" :key="filter.key" type="button" @click="$emit('clearFilter', filter.key)">{{ filter.label }} <span aria-hidden="true">×</span></button>
    </div>
  </form>
</template>
