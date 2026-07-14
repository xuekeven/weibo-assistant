<script setup lang="ts">
import { computed } from 'vue'
import { Button as AButton, DatePicker, TimePicker } from 'ant-design-vue'
import dayjs, { type Dayjs } from 'dayjs'
import 'dayjs/locale/zh-cn'

dayjs.locale('zh-cn')

defineProps<{
  submitting: boolean
  error: string
}>()

const from = defineModel<string>('from', { required: true })
const to = defineModel<string>('to', { required: true })

const fromDate = computed<Dayjs | undefined>({
  get: () => parseLocalDateTime(from.value).date,
  set: (value) => {
    from.value = setLocalDatePart(from.value, value, true)
  }
})

const fromTime = computed<Dayjs | undefined>({
  get: () => parseLocalDateTime(from.value).time,
  set: (value) => {
    from.value = setLocalTimePart(from.value, value, true)
  }
})

const toDate = computed<Dayjs | undefined>({
  get: () => parseLocalDateTime(to.value).date,
  set: (value) => {
    to.value = setLocalDatePart(to.value, value, false)
  }
})

const toTime = computed<Dayjs | undefined>({
  get: () => parseLocalDateTime(to.value).time,
  set: (value) => {
    to.value = setLocalTimePart(to.value, value, false)
  }
})

defineEmits<{
  close: []
  submit: []
}>()

function parseLocalDateTime(value: string) {
  if (!value) return { date: undefined, time: undefined }
  const parsed = dayjs(value)
  if (!parsed.isValid()) return { date: undefined, time: undefined }

  return {
    date: parsed.startOf('day'),
    time: parsed.second(0).millisecond(0)
  }
}

function setLocalDatePart(current: string, date: Dayjs | undefined, required: boolean) {
  const parts = parseLocalDateTime(current)
  if (!date) return required ? '' : ''
  const time = parts.time || dayjs().hour(0).minute(0).second(0).millisecond(0)
  return combineLocalDateTime(date, time)
}

function setLocalTimePart(current: string, time: Dayjs | undefined, required: boolean) {
  const parts = parseLocalDateTime(current)
  if (!time && !parts.date) return required ? '' : ''
  if (!required && !parts.date) return ''
  const date = parts.date || dayjs().startOf('day')
  const nextTime = time || dayjs().hour(0).minute(0).second(0).millisecond(0)
  return combineLocalDateTime(date, nextTime)
}

function combineLocalDateTime(date: Dayjs, time: Dayjs) {
  return date.hour(time.hour()).minute(time.minute()).second(0).millisecond(0).format('YYYY-MM-DDTHH:mm')
}
</script>

<template>
  <div class="history-dialog-backdrop" role="presentation" @click.self="$emit('close')">
    <section class="history-dialog" role="dialog" aria-modal="true" aria-labelledby="history-dialog-title">
      <header>
        <div>
          <h3 id="history-dialog-title">消息回溯</h3>
        </div>
        <UButton color="neutral" variant="ghost" icon="i-lucide-x" aria-label="关闭" @click="$emit('close')" />
      </header>

      <div class="history-form">
        <label>
          <span class="history-field-heading">
            <strong>最早消息时间</strong>
            <small>会从最新消息向前同步，直到早于这个时间。</small>
          </span>
          <div class="history-date-time">
            <DatePicker
              v-model:value="fromDate"
              class="history-date-picker"
              size="large"
              format="YYYY-MM-DD"
              placeholder="选择日期"
              :allow-clear="false"
            />
            <TimePicker
              v-model:value="fromTime"
              class="history-time-picker"
              size="large"
              format="HH:mm"
              placeholder="选择时间"
              :allow-clear="false"
            />
          </div>
        </label>
        <label>
          <span class="history-field-heading">
            <strong>最晚消息时间 <em>可选</em></strong>
            <small>留空时保存从最新消息开始的回溯结果。</small>
          </span>
          <div class="history-date-time">
            <DatePicker
              v-model:value="toDate"
              class="history-date-picker"
              size="large"
              format="YYYY-MM-DD"
              placeholder="不限制日期"
              allow-clear
            />
            <TimePicker
              v-model:value="toTime"
              class="history-time-picker"
              size="large"
              format="HH:mm"
              placeholder="不限制时间"
              :disabled="!toDate"
              allow-clear
            />
          </div>
        </label>
      </div>

      <p v-if="error" class="history-error" role="alert">{{ error }}</p>

      <footer>
        <AButton size="large" class="history-footer-button" @click="$emit('close')">取消</AButton>
        <AButton
          type="primary"
          size="large"
          class="history-footer-button primary"
          :loading="submitting"
          :disabled="submitting || !from"
          @click="$emit('submit')"
        >
          开始回溯
        </AButton>
      </footer>
    </section>
  </div>
</template>
