<script setup lang="ts">
import { computed, ref } from 'vue'

const props = defineProps<{ days: string[] }>()
const emit = defineEmits<{ close: []; select: [date: string] }>()

const today = new Date()
const year = ref(today.getFullYear())
const month = ref(today.getMonth() + 1)
const selectedDate = ref('')
const weekdays = ['一', '二', '三', '四', '五', '六', '日']
const availableDates = computed(() => new Set(props.days))
const years = computed(() => {
  const earliest = props.days.length ? Number(props.days[0].slice(0, 4)) : today.getFullYear()
  const latest = props.days.length ? Number(props.days.at(-1)!.slice(0, 4)) : today.getFullYear()
  return Array.from({ length: Math.max(today.getFullYear(), latest) - earliest + 1 }, (_, index) => earliest + index)
})
const yearOptions = computed(() => years.value.map((item) => ({ value: item, label: `${item} 年` })))
const monthOptions = Array.from({ length: 12 }, (_, index) => ({ value: index + 1, label: `${index + 1} 月` }))
const cells = computed(() => {
  const leading = (new Date(year.value, month.value - 1, 1).getDay() + 6) % 7
  const total = new Date(year.value, month.value, 0).getDate()
  const result: Array<{ day: number; date: string; available: boolean } | null> = Array.from({ length: leading }, () => null)
  for (let day = 1; day <= total; day++) {
    const date = `${year.value}-${String(month.value).padStart(2, '0')}-${String(day).padStart(2, '0')}`
    result.push({ day, date, available: availableDates.value.has(date) })
  }
  return result
})

function changeMonth() {
  selectedDate.value = ''
}

function scrollToSelectedMonth(open: boolean) {
  if (!open) return
  window.setTimeout(() => {
    const dropdown = document.querySelector('.history-month-dropdown')
    const list = dropdown?.querySelector<HTMLElement>('.rc-virtual-list-holder')
    const selected = dropdown?.querySelector<HTMLElement>('.ant-select-item-option-selected')
    if (!list || !selected) return
    const itemTop = selected.getBoundingClientRect().top - list.getBoundingClientRect().top + list.scrollTop
    list.scrollTop = itemTop - (list.clientHeight - selected.clientHeight) / 2
  }, 250)
}
</script>

<template>
  <div class="history-calendar-backdrop" @click.self="emit('close')" @keydown.esc="emit('close')">
    <section class="history-calendar-dialog" role="dialog" aria-modal="true" aria-labelledby="history-calendar-title">
      <h3 id="history-calendar-title">选择消息日期</h3>
      <div class="history-calendar-selects">
        <a-select v-model:value="year" aria-label="年份" size="large" class="history-calendar-select" :options="yearOptions" :list-item-height="40" popup-class-name="filter-select-dropdown" @change="changeMonth" />
        <a-select v-model:value="month" aria-label="月份" size="large" class="history-calendar-select" :options="monthOptions" :virtual="false" :list-item-height="40" popup-class-name="filter-select-dropdown history-month-dropdown" @change="changeMonth" @dropdown-visible-change="scrollToSelectedMonth" />
      </div>
      <div class="history-calendar-grid" aria-label="有聊天记录的日期可选择">
        <span v-for="weekday in weekdays" :key="weekday" class="history-calendar-weekday">{{ weekday }}</span>
        <template v-for="(cell, index) in cells" :key="index">
          <span v-if="!cell" aria-hidden="true"></span>
          <button
            v-else
            type="button"
            class="history-calendar-day"
            :class="{ selected: selectedDate === cell.date }"
            :disabled="!cell.available"
            :aria-label="`${cell.date}${cell.available ? '，有聊天记录' : '，没有聊天记录'}`"
            @click="selectedDate = cell.date"
          >{{ cell.day }}</button>
        </template>
      </div>
      <div class="history-calendar-actions">
        <button type="button" class="history-calendar-cancel" @click="emit('close')">取消</button>
        <button type="button" class="history-calendar-confirm" :disabled="!selectedDate" @click="emit('select', selectedDate)">确定</button>
      </div>
    </section>
  </div>
</template>
