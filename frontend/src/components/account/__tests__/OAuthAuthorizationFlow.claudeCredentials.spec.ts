import { mount } from '@vue/test-utils'
import OAuthAuthorizationFlow from '@/components/account/OAuthAuthorizationFlow.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copied: false,
    copyToClipboard: vi.fn()
  })
}))

describe('OAuthAuthorizationFlow Claude credentials import', () => {
  it('emits trimmed credentials from the Claude-only paste mode', async () => {
    const wrapper = mount(OAuthAuthorizationFlow, {
      props: {
        addMethod: 'oauth',
        platform: 'anthropic',
        showCookieOption: false,
        showClaudeCredentialsOption: true
      },
      global: {
        stubs: { Icon: true }
      }
    })

    const pasteOption = wrapper.find('input[value="claude_credentials"]')
    expect(pasteOption.exists()).toBe(true)
    await pasteOption.setValue()

    const textarea = wrapper.find('textarea[autocomplete="off"]')
    await textarea.setValue('  {"access_token":"token"}  ')
    await wrapper.find('button.btn-primary').trigger('click')

    expect(wrapper.emitted('import-claude-credentials')).toEqual([
      ['{"access_token":"token"}']
    ])
  })

  it('hides the paste mode when disabled', () => {
    const wrapper = mount(OAuthAuthorizationFlow, {
      props: {
        addMethod: 'oauth',
        platform: 'anthropic',
        showCookieOption: false,
        showClaudeCredentialsOption: false
      },
      global: {
        stubs: { Icon: true }
      }
    })

    expect(wrapper.find('input[value="claude_credentials"]').exists()).toBe(false)
  })
})
