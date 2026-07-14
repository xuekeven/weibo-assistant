<script setup lang="ts">
type Option = { label: string; value: string }
type FilterLabel = { key: string; label: string }

defineProps<{
  senderOptions: Option[]
  timeRangeOptions: Option[]
  activeFilters: FilterLabel[]
  loading: boolean
  hasActiveFilters: boolean
}>()

const query = defineModel<string>('query', { required: true })
const sender = defineModel<string>('sender', { required: true })
const timeRange = defineModel<string>('timeRange', { required: true })
const customDate = defineModel<string>('customDate', { required: true })

defineEmits<{
  apply: []
  clear: []
  clearFilter: [key: string]
}>()
</script>

<template>
  <form class="filters" @submit.prevent="$emit('apply')">
    <label class="filter-field keyword-field"><span>搜索</span><UInput v-model="query" size="md" icon="i-lucide-search" placeholder="搜索消息内容或发送人" :ui="{ base: 'text-base' }" class="message-search-input w-full" /></label>
    <label class="filter-field sender-field">
      <span>发送人</span>
      <UInputMenu v-model="sender" size="md" :items="senderOptions" value-key="value" label-key="label" placeholder="搜索发送人" open-on-focus :ui="{ base: 'text-base', item: 'text-base', empty: 'text-base' }" class="sender-input-menu w-full">
        <template #empty>没有匹配的发送人</template>
      </UInputMenu>
    </label>
    <label class="filter-field compact-field"><span>时间</span><USelect v-model="timeRange" size="md" :items="timeRangeOptions" :ui="{ base: 'text-base', item: 'text-base' }" class="w-full" /></label>
    <label v-if="timeRange === 'custom'" class="filter-field compact-field custom-date-field"><span>开始日期</span><UInput v-model="customDate" size="md" type="date" :ui="{ base: 'text-base' }" class="w-full" /></label>
    <div class="filter-actions">
      <UButton class="filter-submit" size="md" type="submit" icon="i-lucide-search" :loading="loading" label="查询" />
      <UButton v-if="hasActiveFilters" class="filter-reset" size="md" type="button" color="neutral" variant="ghost" :disabled="loading" label="清空" @click="$emit('clear')" />
    </div>
    <div v-if="activeFilters.length" class="active-filters" aria-label="当前筛选条件">
      <button v-for="filter in activeFilters" :key="filter.key" type="button" @click="$emit('clearFilter', filter.key)">{{ filter.label }} <span aria-hidden="true">×</span></button>
    </div>
  </form>
</template>
