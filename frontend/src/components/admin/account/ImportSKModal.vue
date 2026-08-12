<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.skImportTitle')"
    width="normal"
    close-on-click-outside
    @close="handleClose"
  >
    <form id="import-sk-form" class="space-y-4" @submit.prevent="handleImport">
      <div class="text-sm text-gray-600 dark:text-dark-300">
        {{ t('admin.accounts.skImportHint') }}
      </div>
      <div
        class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs text-amber-700 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-300"
      >
        {{ t('admin.accounts.skImportWarning') }}
      </div>

      <div>
        <label class="input-label">{{ t('admin.accounts.skImportContent') }}</label>
        <textarea
          v-model="content"
          class="input min-h-[160px] font-mono text-xs"
          :placeholder="t('admin.accounts.skImportContentPlaceholder')"
          :disabled="importing || probing"
        />
        <p class="input-hint">{{ t('admin.accounts.skImportContentHint', { count: skCount }) }}</p>
      </div>

      <div>
        <label class="input-label">{{ t('admin.accounts.skImportCookie') }}</label>
        <textarea
          v-model="cookie"
          class="input min-h-[76px] font-mono text-xs"
          :placeholder="t('admin.accounts.skImportCookiePlaceholder')"
          :disabled="importing || probing"
        />
        <p class="input-hint">{{ t('admin.accounts.skImportCookieHint') }}</p>
      </div>

      <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-700">
        <div class="flex flex-col gap-2 md:flex-row md:items-center md:justify-between">
          <div>
            <div class="text-sm font-medium text-gray-900 dark:text-white">
              {{ t('admin.accounts.skConvertCookieTitle') }}
            </div>
            <div class="mt-1 text-xs text-gray-500 dark:text-dark-400">
              {{ t('admin.accounts.skConvertCookieHint') }}
            </div>
          </div>
          <div class="flex shrink-0 gap-2">
            <button
              class="btn btn-secondary btn-sm"
              type="button"
              :disabled="importing || probing || savingCookie || clearingCookie || !cookie.trim()"
              @click="handleSaveConvertCookie"
            >
              {{ savingCookie ? t('admin.accounts.skConvertCookieSaving') : t('admin.accounts.skConvertCookieSave') }}
            </button>
            <button
              v-if="convertCookieStatus?.source === 'db'"
              class="btn btn-ghost btn-sm text-red-600 dark:text-red-400"
              type="button"
              :disabled="importing || probing || savingCookie || clearingCookie"
              @click="handleClearConvertCookie"
            >
              {{ clearingCookie ? t('admin.accounts.skConvertCookieClearing') : t('admin.accounts.skConvertCookieClear') }}
            </button>
          </div>
        </div>
        <div
          class="mt-3 rounded-md p-3 text-xs"
          :class="convertCookieStatus?.configured
            ? 'bg-green-50 text-green-700 dark:bg-green-900/20 dark:text-green-300'
            : 'bg-amber-50 text-amber-700 dark:bg-amber-900/20 dark:text-amber-300'"
        >
          <template v-if="loadingCookieStatus">{{ t('common.loading') }}</template>
          <template v-else-if="convertCookieStatus">
            <div class="font-medium">
              {{ t('admin.accounts.skConvertCookieSource') }}:
              {{ t(`admin.accounts.skConvertCookieSource_${convertCookieStatus.source}`) }}
            </div>
            <div
              class="mt-2 rounded p-2"
              :class="convertCookieStatus.convert_url_configured
                ? (convertCookieStatus.convert_url_trusted_local
                  ? 'bg-green-100/70 text-green-800 dark:bg-green-900/30 dark:text-green-200'
                  : 'bg-amber-100/70 text-amber-800 dark:bg-amber-900/30 dark:text-amber-200')
                : 'bg-blue-100/70 text-blue-800 dark:bg-blue-900/30 dark:text-blue-200'"
            >
              <div class="font-medium">{{ t('admin.accounts.skConvertURL') }}</div>
              <div v-if="convertCookieStatus.convert_url_configured">
                {{ t('admin.accounts.skConvertURLConfigured', { host: convertCookieStatus.convert_url_host || '-' }) }}
              </div>
              <div v-else>
                {{ t('admin.accounts.skConvertURLNotConfigured') }}
              </div>
              <div v-if="convertCookieStatus.convert_url_configured && convertCookieStatus.convert_url_trusted_local" class="mt-1">
                {{ t('admin.accounts.skConvertURLLocalTrusted') }}
              </div>
              <div v-else-if="convertCookieStatus.convert_url_configured" class="mt-1 font-medium">
                {{ t('admin.accounts.skConvertURLRemoteWarning') }}
              </div>
            </div>
            <div v-if="convertCookieStatus.email || convertCookieStatus.user_id" class="mt-1 font-mono">
              {{ convertCookieStatus.email || '-' }}
              <span v-if="convertCookieStatus.user_id">(uid: {{ convertCookieStatus.user_id }})</span>
            </div>
            <div v-if="convertCookieStatus.expires_at" class="mt-1 font-mono">
              {{ t('admin.accounts.skConvertCookieExpiresAt') }}: {{ formatCookieExpiry(convertCookieStatus.expires_at) }}
            </div>
            <div v-if="!convertCookieStatus.configured" class="mt-1">
              {{ t('admin.accounts.skConvertCookieNone') }}
            </div>
          </template>
        </div>
      </div>

      <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-700">
        <div class="flex flex-col gap-2 md:flex-row md:items-center md:justify-between">
          <div>
            <div class="text-sm font-medium text-gray-900 dark:text-white">转换接口检测</div>
            <div class="mt-1 text-xs text-gray-500 dark:text-dark-400">
              用第一条 SK 检测转换站和 Cookie 是否可用
            </div>
          </div>
          <button
            class="btn btn-secondary btn-sm"
            type="button"
            :disabled="importing || probing || skCount === 0 || !canUseConverter"
            @click="handleProbe"
          >
            {{ probing ? '检测中...' : '检测接口/Cookie' }}
          </button>
        </div>
        <div
          v-if="probeResult"
          class="mt-3 rounded-md p-3 text-sm"
          :class="probeResult.ok ? 'bg-green-50 text-green-700 dark:bg-green-900/20 dark:text-green-300' : 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300'"
        >
          <div class="font-medium">{{ probeResult.ok ? '可用' : '不可用' }}: {{ probeResult.message }}</div>
          <div v-if="probeResult.kind" class="mt-1 font-mono text-xs">kind: {{ probeResult.kind }}</div>
          <div v-if="probeResult.needs_cookie_refresh" class="mt-1 font-medium">
            处理建议：更新转换站 Cookie / cf_clearance，然后重新检测。
          </div>
          <div v-else-if="probeResult.retryable" class="mt-1">
            这是临时失败，后台刷新会冷却后自动重试，不会立刻把账号判死。
          </div>
          <div v-else-if="!probeResult.ok" class="mt-1">
            这是永久失败类型，请按提示更换 SK、账号或检查订阅状态。
          </div>
          <div v-if="probeResult.email || probeResult.subscription_type" class="mt-1 font-mono text-xs">
            {{ probeResult.email || '-' }} {{ probeResult.subscription_type || '' }}
          </div>
          <div v-if="probeResult.details" class="mt-2 max-h-24 overflow-auto rounded bg-white/60 p-2 font-mono text-xs dark:bg-black/20">
            {{ probeResult.details }}
          </div>
        </div>
      </div>

      <div class="grid grid-cols-1 gap-3 md:grid-cols-3">
        <div>
          <label class="input-label">{{ t('admin.accounts.skImportNamePrefix') }}</label>
          <input v-model="namePrefix" class="input" :disabled="importing || probing" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.accounts.skImportConcurrency') }}</label>
          <input v-model.number="concurrency" class="input" type="number" min="0" :disabled="importing || probing" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.accounts.skImportPriority') }}</label>
          <input v-model.number="priority" class="input" type="number" min="0" :disabled="importing || probing" />
        </div>
      </div>

      <div
        v-if="result"
        class="space-y-2 rounded-xl border border-gray-200 p-4 dark:border-dark-700"
      >
        <div class="text-sm font-medium text-gray-900 dark:text-white">
          {{ t('admin.accounts.skImportResult') }}
        </div>
        <div class="text-sm text-gray-700 dark:text-dark-300">
          {{ t('admin.accounts.skImportResultSummary', resultSummary) }}
        </div>
        <div v-if="errorItems.length" class="mt-2">
          <div class="text-sm font-medium text-red-600 dark:text-red-400">
            {{ t('admin.accounts.dataImportErrors') }}
          </div>
          <div class="mt-2 max-h-48 overflow-auto rounded-lg bg-gray-50 p-3 font-mono text-xs dark:bg-dark-800">
            <div v-for="(item, idx) in errorItems" :key="idx" class="whitespace-pre-wrap">
              {{ item.kind }} {{ item.name || item.proxy_key || '-' }} - {{ item.message }}
            </div>
          </div>
        </div>
      </div>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button class="btn btn-secondary" type="button" :disabled="importing || probing" @click="handleClose">
          {{ t('common.cancel') }}
        </button>
        <button class="btn btn-primary" type="submit" form="import-sk-form" :disabled="importing || probing || !canUseConverter">
          {{ importing ? t('admin.accounts.skImporting') : t('admin.accounts.skImportButton') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import type { ConvertCookieStatus, SKConvertProbeResult, SKImportResult } from '@/types'

interface Props {
  show: boolean
}

interface Emits {
  (e: 'close'): void
  (e: 'imported'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const { t } = useI18n()
const appStore = useAppStore()

const importing = ref(false)
const probing = ref(false)
const content = ref('')
const cookie = ref('')
const namePrefix = ref('claude-oauth')
const concurrency = ref(10)
const priority = ref(1)
const result = ref<SKImportResult | null>(null)
const probeResult = ref<SKConvertProbeResult | null>(null)
const hasCreatedData = ref(false)
const convertCookieStatus = ref<ConvertCookieStatus | null>(null)
const loadingCookieStatus = ref(false)
const savingCookie = ref(false)
const clearingCookie = ref(false)

const skCount = computed(() => parseSKs(content.value).length)
const errorItems = computed(() => result.value?.errors || [])
const resultSummary = computed(() => toImportSummary(result.value))
const canUseConverter = computed(() => Boolean(convertCookieStatus.value?.convert_url_configured))

watch(
  () => props.show,
  (open) => {
    if (open) {
      content.value = ''
      cookie.value = ''
      namePrefix.value = 'claude-oauth'
      concurrency.value = 10
      priority.value = 1
      result.value = null
      probeResult.value = null
      hasCreatedData.value = false
      loadConvertCookieStatus()
    }
  }
)

const parseSKs = (value: string) =>
  value
    .split(/[\s,;]+/)
    .map((item) => item.trim())
    .filter(Boolean)

const toImportSummary = (value: SKImportResult | null) => ({
  converted: value?.converted ?? 0,
  convert_failed: value?.convert_failed ?? 0,
  account_created: value?.account_created ?? 0,
  account_failed: value?.account_failed ?? 0
})

const handleClose = () => {
  if (importing.value || probing.value) return
  if (hasCreatedData.value) {
    hasCreatedData.value = false
    emit('imported')
    return
  }
  emit('close')
}

const handleImport = async () => {
  const sks = parseSKs(content.value)
  if (sks.length === 0) {
    appStore.showError(t('admin.accounts.skImportMissingSK'))
    return
  }
  if (!canUseConverter.value) {
    appStore.showError(t('admin.accounts.skConvertURLNotConfigured'))
    return
  }

  importing.value = true
  try {
    const res = await adminAPI.accounts.importFromSK({
      sks,
      cookie: cookie.value.trim() || undefined,
      name_prefix: namePrefix.value.trim() || undefined,
      concurrency: Number.isFinite(concurrency.value) ? concurrency.value : 10,
      priority: Number.isFinite(priority.value) ? priority.value : 1,
      rate_multiplier: 1,
      skip_default_group_bind: true
    })

    result.value = res
    if (res.account_created > 0) {
      hasCreatedData.value = true
    }

    if (res.account_failed > 0 || res.convert_failed > 0) {
      appStore.showError(t('admin.accounts.skImportCompletedWithErrors', toImportSummary(res)))
    } else {
      appStore.showSuccess(t('admin.accounts.skImportSuccess', toImportSummary(res)))
      hasCreatedData.value = false
      emit('imported')
    }
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.skImportFailed'))
  } finally {
    importing.value = false
  }
}

const handleProbe = async () => {
  const sks = parseSKs(content.value)
  if (sks.length === 0) {
    appStore.showError(t('admin.accounts.skImportMissingSK'))
    return
  }
  if (!canUseConverter.value) {
    appStore.showError(t('admin.accounts.skConvertURLNotConfigured'))
    return
  }

  probing.value = true
  probeResult.value = null
  try {
    const res = await adminAPI.accounts.probeSKConverter({
      sk: sks[0],
      cookie: cookie.value.trim() || undefined
    })
    probeResult.value = res
    if (res.ok) {
      appStore.showSuccess('转换接口可用')
    } else if (res.needs_cookie_refresh) {
      appStore.showError('转换站 Cookie 无效，请更新 Cookie/cf_clearance')
    } else {
      appStore.showError(res.message || '转换接口检测失败')
    }
  } catch (error: any) {
    appStore.showError(error?.message || '转换接口检测失败')
  } finally {
    probing.value = false
  }
}

const formatCookieExpiry = (seconds: number) => {
  if (!seconds) return '-'
  return new Date(seconds * 1000).toLocaleString()
}

const loadConvertCookieStatus = async () => {
  loadingCookieStatus.value = true
  try {
    convertCookieStatus.value = await adminAPI.accounts.getConvertCookieStatus()
  } catch (error: any) {
    convertCookieStatus.value = null
    appStore.showError(error?.message || t('admin.accounts.skConvertCookieLoadFailed'))
  } finally {
    loadingCookieStatus.value = false
  }
}

const handleSaveConvertCookie = async () => {
  const value = cookie.value.trim()
  if (!value) {
    appStore.showError(t('admin.accounts.skConvertCookieEmpty'))
    return
  }
  savingCookie.value = true
  try {
    convertCookieStatus.value = await adminAPI.accounts.updateConvertCookie({ cookie: value })
    appStore.showSuccess(t('admin.accounts.skConvertCookieSaved'))
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.skConvertCookieSaveFailed'))
  } finally {
    savingCookie.value = false
  }
}

const handleClearConvertCookie = async () => {
  clearingCookie.value = true
  try {
    convertCookieStatus.value = await adminAPI.accounts.clearConvertCookie()
    appStore.showSuccess(t('admin.accounts.skConvertCookieCleared'))
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.skConvertCookieClearFailed'))
  } finally {
    clearingCookie.value = false
  }
}
</script>
