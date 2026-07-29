<script setup lang="ts">
import { SearchOutlined } from '@ant-design/icons-vue'
import dayjs, { type Dayjs } from 'dayjs'
import { ref } from 'vue'

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

const emit = defineEmits<{
  apply: []
  clear: []
  clearFilter: [key: string]
  senderChange: []
  viewTargetSender: []
}>()

const customDateDialogOpen = ref(false)
const draftCustomDate = ref<Dayjs>(dayjs().startOf('day'))

function handleTimeRangeChange(value: string) {
  if (value === 'custom') {
    const current = customDate.value ? dayjs(customDate.value) : dayjs()
    draftCustomDate.value = current.isValid() ? current.startOf('day') : dayjs().startOf('day')
    customDateDialogOpen.value = true
    return
  }
  timeRange.value = value
  emit('apply')
}

function confirmCustomDate() {
  customDate.value = draftCustomDate.value.format('YYYY-MM-DD')
  timeRange.value = 'custom'
  customDateDialogOpen.value = false
  emit('apply')
}
</script>

<template>
  <form class="filters" @submit.prevent="$emit('apply')">
    <label class="filter-field keyword-field"><span>关键词</span><a-input v-model:value="query" size="large" placeholder="模糊搜索消息正文" class="message-search-input"><template #prefix><SearchOutlined /></template></a-input></label>
    <label class="filter-field sender-field">
      <span>发送人</span>
      <a-select v-model:value="sender" size="large" show-search :options="senderOptions" option-filter-prop="label" placeholder="搜索发送人" class="sender-input-menu" not-found-content="没有匹配的发送人" @change="$emit('senderChange')" />
    </label>
    <label class="filter-field compact-field"><span>时间</span><a-select :value="timeRange" size="large" :options="timeRangeOptions" @change="handleTimeRangeChange" /></label>
    <div class="filter-actions">
      <a-button class="filter-submit" type="primary" size="large" :loading="loading" :disabled="!canViewTargetSender" @click="$emit('viewTargetSender')">查看关注</a-button>
      <a-button v-if="hasActiveFilters" class="filter-reset" size="large" :disabled="loading" @click="$emit('clear')">清空</a-button>
    </div>
    <div v-if="activeFilters.length" class="active-filters" aria-label="当前筛选条件">
      <button v-for="filter in activeFilters" :key="filter.key" type="button" @click="$emit('clearFilter', filter.key)">{{ filter.label }} <span aria-hidden="true">×</span></button>
    </div>
  </form>

  <a-modal
    v-model:open="customDateDialogOpen"
    title="选择开始日期"
    centered
    :width="420"
    :mask-closable="false"
    ok-text="确定"
    cancel-text="取消"
    @ok="confirmCustomDate"
  >
    <div class="custom-date-dialog-content">
      <p>选择后，将显示从当天 00:00 开始的消息。</p>
      <label>
        <span>开始日期</span>
        <a-date-picker
          v-model:value="draftCustomDate"
          size="large"
          format="YYYY-MM-DD"
          :allow-clear="false"
          placeholder="选择开始日期"
        />
      </label>
    </div>
  </a-modal>
</template>
