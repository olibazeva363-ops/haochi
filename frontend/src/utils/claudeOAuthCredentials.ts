type UnknownRecord = Record<string, unknown>

export type ClaudeOAuthCredentialsErrorCode =
  | 'invalid_json'
  | 'invalid_payload'
  | 'access_token_missing'
  | 'expires_at_invalid'

export class ClaudeOAuthCredentialsError extends Error {
  constructor(public readonly code: ClaudeOAuthCredentialsErrorCode) {
    super(code)
    this.name = 'ClaudeOAuthCredentialsError'
  }
}

export interface ParsedClaudeOAuthCredentials {
  credentials: Record<string, unknown>
  extra: Record<string, unknown>
}

export function parseClaudeOAuthCredentials(
  input: string,
  nowMs: number = Date.now()
): ParsedClaudeOAuthCredentials {
  let parsed: unknown
  try {
    parsed = JSON.parse(input.trim())
  } catch {
    throw new ClaudeOAuthCredentialsError('invalid_json')
  }

  const root = recordValue(parsed)
  if (!root) {
    throw new ClaudeOAuthCredentialsError('invalid_payload')
  }

  const credentialRoot =
    recordValue(root.claudeAiOauth) ||
    recordValue(root.oauth) ||
    recordValue(root.credentials) ||
    root
  const sources = uniqueRecords([
    credentialRoot,
    recordValue(credentialRoot.tokens),
    root,
    recordValue(root.tokens)
  ])

  const accessToken = firstString(sources, ['access_token', 'accessToken'])
  if (!accessToken) {
    throw new ClaudeOAuthCredentialsError('access_token_missing')
  }

  const credentials: Record<string, unknown> = {
    access_token: accessToken,
    token_type: firstString(sources, ['token_type', 'tokenType']) || 'Bearer'
  }
  const refreshToken = firstString(sources, ['refresh_token', 'refreshToken'])
  if (refreshToken) credentials.refresh_token = refreshToken

  const idToken = firstString(sources, ['id_token', 'idToken'])
  if (idToken) credentials.id_token = idToken

  const scope = firstString(sources, ['scope']) || firstStringArray(sources, ['scopes'])
  if (scope) credentials.scope = scope

  const expiresAtValue = firstValue(sources, ['expires_at', 'expiresAt'])
  const expiresInValue = firstValue(sources, ['expires_in', 'expiresIn'])
  if (expiresAtValue !== undefined || expiresInValue !== undefined) {
    const expiresAt = parseExpiresAt(expiresAtValue, expiresInValue, nowMs)
    if (expiresAt === undefined) {
      throw new ClaudeOAuthCredentialsError('expires_at_invalid')
    }
    credentials.expires_at = expiresAt
  }

  const metadataSources = uniqueRecords([
    root,
    credentialRoot,
    recordValue(root.metadata),
    recordValue(root.oauthAccount),
    recordValue(credentialRoot.oauthAccount)
  ])
  const oauthAccount =
    recordValue(root.oauthAccount) || recordValue(credentialRoot.oauthAccount)
  const organization = recordValue(root.organization) || recordValue(credentialRoot.organization)
  const account = recordValue(root.account) || recordValue(credentialRoot.account)

  const extra: Record<string, unknown> = {}
  const orgUUID =
    firstString(metadataSources, ['org_uuid', 'organizationId', 'organization_id']) ||
    stringValue(organization?.uuid) ||
    stringValue(oauthAccount?.organizationUuid) ||
    stringValue(oauthAccount?.accountUuid)
  const accountUUID =
    firstString(metadataSources, [
      'account_uuid',
      'claudeCodeAccountUuid',
      'claude_code_account_uuid'
    ]) ||
    stringValue(account?.uuid) ||
    stringValue(oauthAccount?.accountUuid)
  const emailAddress =
    firstString(metadataSources, ['email_address', 'accountEmail', 'account_email']) ||
    firstString(account ? [account] : [], ['email_address', 'emailAddress', 'email']) ||
    stringValue(oauthAccount?.emailAddress)
  const claudeUserID = firstString(metadataSources, [
    'claudeCodeUserId',
    'claude_code_user_id',
    'claude_user_id',
    'userID'
  ])
  const subscriptionType = firstString(metadataSources, [
    'rateLimitTier',
    'organizationRateLimitTier',
    'userRateLimitTier',
    'subscriptionType',
    'subscription_type',
    'organizationType'
  ])

  if (orgUUID) extra.org_uuid = orgUUID
  if (accountUUID) extra.account_uuid = accountUUID
  if (emailAddress) extra.email_address = emailAddress
  if (claudeUserID) extra.claude_user_id = claudeUserID
  if (subscriptionType) extra.subscription_type = subscriptionType

  return { credentials, extra }
}

function parseExpiresAt(
  expiresAtValue: unknown,
  expiresInValue: unknown,
  nowMs: number
): number | undefined {
  if (expiresAtValue !== undefined) {
    const numeric = numberValue(expiresAtValue)
    if (numeric !== undefined && numeric > 0) {
      return Math.floor(numeric >= 100_000_000_000 ? numeric / 1000 : numeric)
    }
    if (typeof expiresAtValue === 'string') {
      const timestamp = Date.parse(expiresAtValue)
      if (Number.isFinite(timestamp)) return Math.floor(timestamp / 1000)
    }
    return undefined
  }

  const expiresIn = numberValue(expiresInValue)
  if (expiresIn === undefined || expiresIn <= 0) return undefined
  return Math.floor(nowMs / 1000 + expiresIn)
}

function uniqueRecords(values: Array<UnknownRecord | undefined>): UnknownRecord[] {
  return values.filter(
    (value, index): value is UnknownRecord =>
      Boolean(value) && values.indexOf(value) === index
  )
}

function firstString(sources: UnknownRecord[], keys: string[]): string | undefined {
  for (const source of sources) {
    for (const key of keys) {
      const value = stringValue(source[key])
      if (value) return value
    }
  }
  return undefined
}

function firstStringArray(sources: UnknownRecord[], keys: string[]): string | undefined {
  for (const source of sources) {
    for (const key of keys) {
      const value = source[key]
      if (!Array.isArray(value)) continue
      const items = value.map(stringValue).filter((item): item is string => Boolean(item))
      if (items.length) return items.join(' ')
    }
  }
  return undefined
}

function firstValue(sources: UnknownRecord[], keys: string[]): unknown {
  for (const source of sources) {
    for (const key of keys) {
      if (source[key] !== undefined && source[key] !== null && source[key] !== '') {
        return source[key]
      }
    }
  }
  return undefined
}

function recordValue(value: unknown): UnknownRecord | undefined {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as UnknownRecord)
    : undefined
}

function stringValue(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value.trim() : undefined
}

function numberValue(value: unknown): number | undefined {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value !== 'string' || !value.trim()) return undefined
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : undefined
}
