import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Account } from '@/types'
import ReAuthAccountModal from '../ReAuthAccountModal.vue'

const { applyOAuthCredentials, showError, showSuccess } = vi.hoisted(() => ({
  applyOAuthCredentials: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: { accounts: { applyOAuthCredentials } },
}))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess }),
}))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))

const FlowStub = defineComponent({
  props: { showClaudeCredentialsOption: Boolean },
  emits: ['import-claude-credentials'],
  data: () => ({ inputMethod: 'claude_credentials' }),
  template: '<div />',
})

async function openModal(type: Account['type'] = 'oauth') {
  const account = { id: 17, name: 'Claude', platform: 'anthropic', type, credentials: {} } as Account
  const wrapper = mount(ReAuthAccountModal, {
    props: { show: false, account },
    global: {
      stubs: {
        BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
        OAuthAuthorizationFlow: FlowStub,
        Icon: true,
      },
    },
  })
  await wrapper.setProps({ show: true })
  return { wrapper, account }
}

describe('ReAuthAccountModal Claude credentials', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('updates the existing account from the credentials paste event', async () => {
    const { wrapper, account } = await openModal()
    const updated = { ...account, status: 'active' }
    applyOAuthCredentials.mockResolvedValue(updated)
    const flow = wrapper.getComponent(FlowStub)
    expect(flow.props('showClaudeCredentialsOption')).toBe(true)
    flow.vm.$emit('import-claude-credentials', '{"accessToken":"new-token"}')
    await flushPromises()

    expect(applyOAuthCredentials).toHaveBeenCalledWith(17, expect.objectContaining({
      type: 'oauth', credentials: expect.objectContaining({ access_token: 'new-token' }),
    }))
    expect(wrapper.emitted('reauthorized')).toEqual([[updated]])
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })

  it('reports invalid credentials without replacing the account', async () => {
    const { wrapper } = await openModal()
    wrapper.getComponent(FlowStub).vm.$emit('import-claude-credentials', '{}')
    await flushPromises()

    expect(applyOAuthCredentials).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('admin.accounts.oauth.claudeCredentialsErrors.access_token_missing')
    expect(wrapper.emitted('close')).toBeUndefined()
    wrapper.unmount()
  })

  it('keeps OAuth JSON import hidden for setup-token accounts', async () => {
    const { wrapper } = await openModal('setup-token')
    expect(wrapper.getComponent(FlowStub).props('showClaudeCredentialsOption')).toBe(false)
    wrapper.unmount()
  })
})
