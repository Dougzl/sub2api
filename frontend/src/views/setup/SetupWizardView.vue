<template>
  <div class="flex min-h-screen items-center justify-center bg-gradient-to-br from-gray-50 to-gray-100 p-4 dark:from-dark-900 dark:to-dark-800">
    <div class="w-full max-w-xl">
      <div class="mb-8 text-center">
        <div class="mb-4 inline-flex h-16 w-16 items-center justify-center rounded-2xl bg-gradient-to-br from-primary-500 to-primary-600 shadow-lg">
          <Icon name="cog" size="xl" class="text-white" />
        </div>
        <h1 class="text-3xl font-bold text-gray-900 dark:text-white">{{ t('setup.title') }}</h1>
        <p class="mt-2 text-gray-500 dark:text-dark-400">SQLite 单机模式，无需配置 PostgreSQL 或 Redis</p>
      </div>

      <div class="rounded-2xl bg-white p-8 shadow-xl dark:bg-dark-800">
        <div v-if="!installSuccess" class="space-y-6">
          <div class="rounded-xl border border-blue-200 bg-blue-50 p-4 text-sm text-blue-800 dark:border-blue-800/50 dark:bg-blue-900/20 dark:text-blue-200">
            <div class="flex gap-3">
              <Icon name="database" size="md" class="mt-0.5 flex-shrink-0" />
              <div>
                <p class="font-semibold">内置 SQLite 存储</p>
                <p class="mt-1">安装程序会自动创建 <code>./data/sub2api.db</code>，Redis 已默认关闭。</p>
              </div>
            </div>
          </div>

          <div class="text-center">
            <h2 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('setup.admin.title') }}</h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('setup.admin.description') }}</p>
          </div>

          <div>
            <label class="input-label">{{ t('setup.admin.email') }}</label>
            <input v-model="formData.admin.email" type="email" class="input" placeholder="admin@example.com" />
          </div>

          <div>
            <label class="input-label">{{ t('setup.admin.password') }}</label>
            <input v-model="formData.admin.password" type="password" class="input" :placeholder="t('setup.admin.passwordPlaceholder')" />
          </div>

          <div>
            <label class="input-label">{{ t('setup.admin.confirmPassword') }}</label>
            <input v-model="confirmPassword" type="password" class="input" :placeholder="t('setup.admin.confirmPasswordPlaceholder')" />
            <p v-if="confirmPassword && formData.admin.password !== confirmPassword" class="input-error-text">
              {{ t('setup.admin.passwordMismatch') }}
            </p>
          </div>

          <div v-if="errorMessage" class="rounded-xl border border-red-200 bg-red-50 p-4 dark:border-red-800/50 dark:bg-red-900/20">
            <div class="flex items-start gap-3">
              <Icon name="exclamationCircle" size="md" class="flex-shrink-0 text-red-500" />
              <p class="text-sm text-red-700 dark:text-red-400">{{ errorMessage }}</p>
            </div>
          </div>

          <button @click="performInstall" :disabled="!canInstall || installing" class="btn btn-primary w-full">
            <svg v-if="installing" class="-ml-1 mr-2 h-4 w-4 animate-spin" fill="none" viewBox="0 0 24 24">
              <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
              <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
            </svg>
            {{ installing ? t('setup.status.installing') : t('setup.status.completeInstallation') }}
          </button>
        </div>

        <div v-else class="rounded-xl border border-green-200 bg-green-50 p-4 dark:border-green-800/50 dark:bg-green-900/20">
          <div class="flex items-start gap-3">
            <svg v-if="!serviceReady" class="h-5 w-5 flex-shrink-0 animate-spin text-green-500" fill="none" viewBox="0 0 24 24">
              <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
              <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
            </svg>
            <Icon v-else name="checkCircle" size="md" class="flex-shrink-0 text-green-500" />
            <div>
              <p class="text-sm font-medium text-green-700 dark:text-green-400">{{ t('setup.status.completed') }}</p>
              <p class="mt-1 text-sm text-green-600 dark:text-green-500">
                {{ serviceReady ? t('setup.status.redirecting') : t('setup.status.restarting') }}
              </p>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { install, type InstallRequest } from '@/api/setup'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()

const errorMessage = ref('')
const installSuccess = ref(false)
const installing = ref(false)
const confirmPassword = ref('')
const serviceReady = ref(false)

const getCurrentPort = (): number => {
  const port = window.location.port
  if (port) return parseInt(port, 10)
  return window.location.protocol === 'https:' ? 443 : 80
}

const formData = reactive<InstallRequest>({
  database: {
    engine: 'sqlite',
    host: 'localhost',
    port: 0,
    user: '',
    password: '',
    dbname: './data/sub2api.db',
    sslmode: 'disable'
  },
  redis: {
    enabled: false,
    host: 'localhost',
    port: 6379,
    password: '',
    db: 0,
    enable_tls: false
  },
  admin: {
    email: '',
    password: ''
  },
  server: {
    host: '0.0.0.0',
    port: getCurrentPort(),
    mode: 'release'
  }
})

const canInstall = computed(() =>
  Boolean(
    formData.admin.email &&
      formData.admin.password.length >= 8 &&
      formData.admin.password === confirmPassword.value
  )
)

async function performInstall() {
  installing.value = true
  errorMessage.value = ''

  try {
    await install({
      database: {
        engine: 'sqlite',
        host: 'localhost',
        port: 0,
        user: '',
        password: '',
        dbname: './data/sub2api.db',
        sslmode: 'disable'
      },
      redis: {
        enabled: false,
        host: 'localhost',
        port: 6379,
        password: '',
        db: 0,
        enable_tls: false
      },
      admin: formData.admin,
      server: formData.server
    })
    installSuccess.value = true
    serviceReady.value = true
    setTimeout(() => { window.location.href = '/login' }, 800)
  } catch (error: unknown) {
    const err = error as { response?: { data?: { detail?: string; message?: string } }; message?: string }
    errorMessage.value = err.response?.data?.detail || err.response?.data?.message || err.message || 'Installation failed'
  } finally {
    installing.value = false
  }
}

</script>
