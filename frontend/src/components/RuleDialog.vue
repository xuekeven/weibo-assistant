<script setup lang="ts">
import { CloseOutlined } from '@ant-design/icons-vue'
import type { Group, User } from '../api'
import type { RuleForm } from './ManagementCenter.vue'

defineProps<{
  groups: Group[]
  users: User[]
  editing: boolean
  error: string
}>()

const ruleForm = defineModel<RuleForm>('ruleForm', { required: true })

defineEmits<{
  close: []
  submit: []
}>()
</script>

<template>
  <div class="history-dialog-backdrop" role="presentation" @click.self="$emit('close')">
    <section class="history-dialog rule-dialog" role="dialog" aria-modal="true" aria-labelledby="rule-dialog-title">
      <header>
        <div>
          <h3 id="rule-dialog-title">{{ editing ? '编辑通知规则' : '新建通知规则' }}</h3>
          <p>同一规则中的群聊、发送人和消息类型需要同时满足。</p>
        </div>
        <a-button type="text" aria-label="关闭" @click="$emit('close')"><CloseOutlined /></a-button>
      </header>

      <form class="rule-dialog-form" @submit.prevent="$emit('submit')">
        <div class="rule-dialog-fields">
          <label class="rule-dialog-field">
            <span class="rule-dialog-label">规则名称 <em>必填</em></span>
            <small>用于区分不同的通知条件。</small>
            <a-input v-model:value="ruleForm.name" size="large" placeholder="例如：tombkeeper 通知" />
          </label>
          <label class="rule-dialog-field">
            <span class="rule-dialog-label">群聊</span>
            <small>留空表示匹配全部群聊。</small>
            <a-select v-model:value="ruleForm.groupId" size="large">
              <a-select-option value="">全部群聊</a-select-option>
              <a-select-option v-for="group in groups" :key="group.id" :value="group.weiboGroupId">{{ group.name }}</a-select-option>
            </a-select>
          </label>
          <label class="rule-dialog-field">
            <span class="rule-dialog-label">发送人</span>
            <small>建议使用稳定的微博用户 ID 对应账号。</small>
            <a-select v-model:value="ruleForm.senderId" size="large" show-search option-filter-prop="label">
              <a-select-option value="" label="全部发送人">全部发送人</a-select-option>
              <a-select-option v-for="user in users" :key="user.id" :value="user.weiboUserId" :label="user.nickname">{{ user.nickname }}</a-select-option>
            </a-select>
          </label>
          <label class="rule-dialog-field">
            <span class="rule-dialog-label">消息类型</span>
            <small>留空表示该发送人的所有消息类型。</small>
            <a-select v-model:value="ruleForm.messageType" size="large">
              <a-select-option value="">全部消息类型</a-select-option>
              <a-select-option value="text">文本</a-select-option>
              <a-select-option value="image">图片</a-select-option>
              <a-select-option value="video">视频</a-select-option>
              <a-select-option value="system">系统消息</a-select-option>
              <a-select-option value="recall">撤回</a-select-option>
              <a-select-option value="unknown">其他</a-select-option>
            </a-select>
          </label>
          <label class="rule-enabled-field">
            <a-checkbox v-model:checked="ruleForm.enabled" />
            <span><strong>启用此规则</strong><small>保存后立即用于匹配新收到的消息。</small></span>
          </label>
          <p v-if="error" class="history-error rule-dialog-error" role="alert">{{ error }}</p>
        </div>
        <footer class="rule-dialog-actions">
          <a-button size="large" class="history-footer-button" @click="$emit('close')">取消</a-button>
          <a-button type="primary" html-type="submit" size="large" class="history-footer-button primary" :disabled="!ruleForm.name.trim()">
            {{ editing ? '保存修改' : '创建规则' }}
          </a-button>
        </footer>
      </form>
    </section>
  </div>
</template>
