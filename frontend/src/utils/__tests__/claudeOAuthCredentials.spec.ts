import {
  ClaudeOAuthCredentialsError,
  parseClaudeOAuthCredentials
} from '@/utils/claudeOAuthCredentials'

describe('parseClaudeOAuthCredentials', () => {
  it('imports Claude Code credentials and account metadata', () => {
    const result = parseClaudeOAuthCredentials(
      JSON.stringify({
        claudeAiOauth: {
          accessToken: ' anthropic-access ',
          refreshToken: 'anthropic-refresh',
          idToken: 'anthropic-id',
          expiresAt: 1_800_000_000_000,
          scopes: ['user:profile', 'user:inference'],
          rateLimitTier: 'default_claude_max_5x'
        },
        userID: 'claude-user',
        oauthAccount: {
          accountUuid: 'account-uuid',
          organizationUuid: 'org-uuid',
          emailAddress: 'claude@example.com'
        }
      })
    )

    expect(result.credentials).toEqual({
      access_token: 'anthropic-access',
      refresh_token: 'anthropic-refresh',
      id_token: 'anthropic-id',
      token_type: 'Bearer',
      scope: 'user:profile user:inference',
      expires_at: 1_800_000_000
    })
    expect(result.extra).toEqual({
      org_uuid: 'org-uuid',
      account_uuid: 'account-uuid',
      email_address: 'claude@example.com',
      claude_user_id: 'claude-user',
      subscription_type: 'default_claude_max_5x'
    })
  })

  it('normalizes snake_case token exports and relative expiration', () => {
    const result = parseClaudeOAuthCredentials(
      JSON.stringify({
        tokens: {
          access_token: 'anthropic-access',
          refresh_token: 'anthropic-refresh'
        },
        token_type: 'Bearer',
        expires_in: '3600',
        organization: { uuid: 'org-uuid' },
        account: { uuid: 'account-uuid', email_address: 'claude@example.com' },
        subscription_type: 'claude_pro'
      }),
      1_700_000_000_000
    )

    expect(result.credentials.expires_at).toBe(1_700_003_600)
    expect(result.extra).toMatchObject({
      org_uuid: 'org-uuid',
      account_uuid: 'account-uuid',
      email_address: 'claude@example.com',
      subscription_type: 'claude_pro'
    })
  })

  it('allows an access-token-only Anthropic export', () => {
    expect(parseClaudeOAuthCredentials('{"access_token":"anthropic-access"}')).toEqual({
      credentials: {
        access_token: 'anthropic-access',
        token_type: 'Bearer'
      },
      extra: {}
    })
  })

  it.each([
    ['not-json', 'invalid_json'],
    ['[]', 'invalid_payload'],
    ['{}', 'access_token_missing'],
    ['{"access_token":"token","expires_at":"bad"}', 'expires_at_invalid']
  ])('rejects invalid input with a stable code', (input, code) => {
    try {
      parseClaudeOAuthCredentials(input)
      throw new Error('expected parser to reject')
    } catch (error) {
      expect(error).toBeInstanceOf(ClaudeOAuthCredentialsError)
      expect((error as ClaudeOAuthCredentialsError).code).toBe(code)
    }
  })
})
