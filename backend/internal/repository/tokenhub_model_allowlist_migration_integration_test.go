//go:build integration

package repository

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Frozen from 44280114c, before adding the model allowlist migrations.
//
//go:embed testdata/tokenhub_021_migrations.txt
var tokenHub021MigrationManifest string

const tokenHubModelAllowlistMigration = "235_group_model_allowlist.sql"

const tokenHub021GroupPricing = `[{"model":"claude-sonnet-4-5","input_price":1.23,"output_price":4.56}]`
const tokenHub021SKCredentials = `{"claude_sk":"test-fixture","access_token":"test-only-token","refresh_token":"test-only-refresh"}`
const tokenHub021FrozenExtra = `{"claude_frozen_environment_profile":{"schema":1,"source":"simulated","user_agent":"claude-cli/2.1.258 (external, cli)","device_id":"test-device","proxy_id":19},"local_setting":"preserved"}`

var tokenHub021AllowlistFixtures = []struct {
	name    string
	config  string
	enabled bool
}{
	{"tokenhub-021-enabled", `{"enabled":true,"models":["claude-sonnet-4-5","claude-haiku-*"]}`, true},
	{"tokenhub-021-disabled", `{"enabled":false,"models":["claude-sonnet-4-5"]}`, false},
	{"tokenhub-021-empty", `{}`, false},
}

func TestTokenHubUpgradeFrom021ModelAllowlistPreservesDataAndRepairsRecorded235(t *testing.T) {
	for _, recorded235 := range []bool{false, true} {
		name := "normal_upgrade"
		if recorded235 {
			name = "recorded_235_with_legacy_column"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			db := newTokenHubAllowlistUpgradeDB(ctx, t)
			baseline := tokenHub021MigrationFS(t)
			require.NoError(t, applyMigrationsFS(ctx, db, baseline))
			var applied int
			require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&applied))
			require.Equal(t, 281, applied)
			seedTokenHub021AllowlistData(ctx, t, db)

			var originalChecksum string
			var originalAppliedAt time.Time
			if recorded235 {
				body, err := migrations.FS.ReadFile(tokenHubModelAllowlistMigration)
				require.NoError(t, err)
				baseline[tokenHubModelAllowlistMigration] = &fstest.MapFile{Data: body, Mode: 0444}
				require.NoError(t, applyMigrationsFS(ctx, db, baseline))
				require.NoError(t, db.QueryRowContext(ctx,
					"SELECT checksum, applied_at FROM schema_migrations WHERE filename = $1",
					tokenHubModelAllowlistMigration).Scan(&originalChecksum, &originalAppliedAt))
				// A partial rollback can restore the old column without removing
				// 235 from the migration ledger. Only 236 should repair that state.
				_, err = db.ExecContext(ctx, "ALTER TABLE groups RENAME COLUMN model_allowlist TO models_list_config")
				require.NoError(t, err)
			}

			for startup := 0; startup < 2; startup++ {
				require.NoError(t, ApplyMigrations(ctx, db), "startup %d", startup+1)
				requireTokenHub021AllowlistData(ctx, t, db)
				require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&applied))
				require.Equal(t, 283, applied)
				if recorded235 {
					var checksum string
					var appliedAt time.Time
					require.NoError(t, db.QueryRowContext(ctx,
						"SELECT checksum, applied_at FROM schema_migrations WHERE filename = $1",
						tokenHubModelAllowlistMigration).Scan(&checksum, &appliedAt))
					require.Equal(t, originalChecksum, checksum)
					require.True(t, originalAppliedAt.Equal(appliedAt), "235 must remain recorded without replay")
				}
			}
		})
	}
}

func tokenHub021MigrationFS(t *testing.T) fstest.MapFS {
	t.Helper()
	baseline := fstest.MapFS{}
	for _, name := range strings.Fields(tokenHub021MigrationManifest) {
		body, err := migrations.FS.ReadFile(name)
		require.NoError(t, err)
		baseline[name] = &fstest.MapFile{Data: body, Mode: 0444}
	}
	require.Len(t, baseline, 281)
	return baseline
}

func newTokenHubAllowlistUpgradeDB(ctx context.Context, t *testing.T) *sql.DB {
	t.Helper()
	pg, err := tcpostgres.Run(ctx, selectDockerImage(ctx, postgresImageTag),
		tcpostgres.WithDatabase("tokenhub_allowlist_upgrade_test"),
		tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedTokenHub021AllowlistData(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(ctx, `
INSERT INTO users (email, password_hash, balance, frozen_balance)
VALUES ('tokenhub-021@example.test', 'test-only', 12.34567890, 1.25000000);
INSERT INTO settings (key, value) VALUES ('site_name', 'TokenHub allowlist upgrade')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;
`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
INSERT INTO accounts (name, platform, type, credentials, extra)
VALUES ('tokenhub-021-sk', 'anthropic', 'oauth', $1::jsonb, $2::jsonb)
`, tokenHub021SKCredentials, tokenHub021FrozenExtra)
	require.NoError(t, err)
	for _, fixture := range tokenHub021AllowlistFixtures {
		_, err = db.ExecContext(ctx, `
INSERT INTO groups (name, platform, models_list_config, rate_multiplier,
                    daily_limit_usd, video_price_480p, model_pricing)
VALUES ($1, 'anthropic', $2::jsonb, 1.25, 3.75000000, 0.25, $3::jsonb)
`, fixture.name, fixture.config, tokenHub021GroupPricing)
		require.NoError(t, err)
	}
	_, err = db.ExecContext(ctx, `
INSERT INTO api_keys (user_id, group_id, key, name)
SELECT u.id, g.id, 'sk-upgrade-' || g.name, g.name
FROM users u CROSS JOIN groups g
WHERE u.email = 'tokenhub-021@example.test' AND g.name LIKE 'tokenhub-021-%';
INSERT INTO user_subscriptions (user_id, group_id, starts_at, expires_at, daily_usage_usd)
SELECT u.id, g.id, '2026-09-01 00:00:00+00', '2026-10-01 00:00:00+00', 1.2500000000
FROM users u CROSS JOIN groups g
WHERE u.email = 'tokenhub-021@example.test' AND g.name LIKE 'tokenhub-021-%';
INSERT INTO account_groups (account_id, group_id, priority)
SELECT a.id, g.id, 7 FROM accounts a CROSS JOIN groups g
WHERE a.name = 'tokenhub-021-sk' AND g.name LIKE 'tokenhub-021-%';
INSERT INTO user_allowed_groups (user_id, group_id)
SELECT u.id, g.id FROM users u CROSS JOIN groups g
WHERE u.email = 'tokenhub-021@example.test' AND g.name LIKE 'tokenhub-021-%';
`)
	require.NoError(t, err)
}

func requireTokenHub021AllowlistData(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()
	var balance, frozenBalance, siteName, credentials, extra string
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT balance::text, frozen_balance::text FROM users WHERE email = 'tokenhub-021@example.test'").Scan(&balance, &frozenBalance))
	require.Equal(t, "12.34567890", balance)
	require.Equal(t, "1.25000000", frozenBalance)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = 'site_name'").Scan(&siteName))
	require.Equal(t, "TokenHub allowlist upgrade", siteName)
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT credentials::text, extra::text FROM accounts WHERE name = 'tokenhub-021-sk'").Scan(&credentials, &extra))
	require.JSONEq(t, tokenHub021SKCredentials, credentials)
	require.JSONEq(t, tokenHub021FrozenExtra, extra)

	for _, fixture := range tokenHub021AllowlistFixtures {
		var configJSON, pricing string
		var multiplier, dailyLimit, videoPrice float64
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT model_allowlist::text, model_pricing::text, rate_multiplier, daily_limit_usd, video_price_480p
FROM groups WHERE name = $1
`, fixture.name).Scan(&configJSON, &pricing, &multiplier, &dailyLimit, &videoPrice))
		require.JSONEq(t, fixture.config, configJSON)
		require.JSONEq(t, tokenHub021GroupPricing, pricing)
		require.Equal(t, 1.25, multiplier)
		require.Equal(t, 3.75, dailyLimit)
		require.Equal(t, 0.25, videoPrice, "the already-recorded video cleanup must not replay")
		var allowlist service.GroupModelAllowlist
		require.NoError(t, json.Unmarshal([]byte(configJSON), &allowlist))
		require.Equal(t, fixture.enabled, allowlist.Enabled)
		require.True(t, allowlist.Allows("claude-sonnet-4-5"))
		require.True(t, allowlist.Allows("claude-haiku-4-5"))
		require.Equal(t, !fixture.enabled, allowlist.Allows("model-not-listed"), "enabled display filters now enforce admission")

		var keyAllowlist, subscriptionAllowlist string
		var dailyUsage float64
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT g.model_allowlist::text FROM api_keys k
JOIN groups g ON g.id = k.group_id JOIN users u ON u.id = k.user_id
WHERE k.name = $1 AND u.email = 'tokenhub-021@example.test'
`, fixture.name).Scan(&keyAllowlist))
		require.JSONEq(t, fixture.config, keyAllowlist)
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT g.model_allowlist::text, s.daily_usage_usd FROM user_subscriptions s
JOIN groups g ON g.id = s.group_id JOIN users u ON u.id = s.user_id
WHERE g.name = $1 AND u.email = 'tokenhub-021@example.test'
`, fixture.name).Scan(&subscriptionAllowlist, &dailyUsage))
		require.JSONEq(t, fixture.config, subscriptionAllowlist)
		require.Equal(t, 1.25, dailyUsage)
	}
	var bindings int
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM account_groups ag
JOIN accounts a ON a.id = ag.account_id JOIN groups g ON g.id = ag.group_id
JOIN user_allowed_groups ug ON ug.group_id = g.id JOIN users u ON u.id = ug.user_id
WHERE a.name = 'tokenhub-021-sk' AND u.email = 'tokenhub-021@example.test' AND ag.priority = 7
`).Scan(&bindings))
	require.Equal(t, 3, bindings)
	var oldColumns int
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM information_schema.columns
WHERE table_schema = 'public' AND table_name = 'groups' AND column_name = 'models_list_config'
`).Scan(&oldColumns))
	require.Zero(t, oldColumns)
	var nullable, defaultValue string
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT is_nullable, column_default FROM information_schema.columns
WHERE table_schema = 'public' AND table_name = 'groups' AND column_name = 'model_allowlist'
`).Scan(&nullable, &defaultValue))
	require.Equal(t, "NO", nullable)
	require.Contains(t, defaultValue, "'{}'::jsonb")
}

// Only the repair SQL is schema-independent; this does not exercise a fresh
// 235 -> 236 upgrade outside public, which 235 itself does not support.
func TestMigration236RepairsTargetSchemaWithoutChangingPublicGroup(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	var publicGroupID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO public.groups (name, platform, model_allowlist)
VALUES ('migration-236-public-untouched', 'anthropic', '{"enabled":true,"models":["public-only"]}'::jsonb)
RETURNING id
`).Scan(&publicGroupID))
	_, err := tx.ExecContext(ctx, `
CREATE SCHEMA tokenhub_allowlist_repair;
SET LOCAL search_path TO tokenhub_allowlist_repair, public;
CREATE TABLE groups (id BIGINT PRIMARY KEY, models_list_config JSONB);
INSERT INTO groups VALUES
  (1, '{"enabled":true,"models":["private-only"]}'::jsonb),
  (2, NULL);
`)
	require.NoError(t, err)
	for replay := 0; replay < 2; replay++ {
		applyGroupModelAllowlistRepair(ctx, t, tx)
		var privateConfig, emptyConfig, publicConfig string
		require.NoError(t, tx.QueryRowContext(ctx, "SELECT model_allowlist::text FROM groups WHERE id = 1").Scan(&privateConfig))
		require.JSONEq(t, `{"enabled":true,"models":["private-only"]}`, privateConfig)
		require.NoError(t, tx.QueryRowContext(ctx, "SELECT model_allowlist::text FROM groups WHERE id = 2").Scan(&emptyConfig))
		require.JSONEq(t, `{}`, emptyConfig)
		require.NoError(t, tx.QueryRowContext(ctx, "SELECT model_allowlist::text FROM public.groups WHERE id = $1", publicGroupID).Scan(&publicConfig))
		require.JSONEq(t, `{"enabled":true,"models":["public-only"]}`, publicConfig)
		var nullable, defaultValue string
		require.NoError(t, tx.QueryRowContext(ctx, `
SELECT is_nullable, column_default FROM information_schema.columns
WHERE table_schema = current_schema() AND table_name = 'groups' AND column_name = 'model_allowlist'
`).Scan(&nullable, &defaultValue))
		require.Equal(t, "NO", nullable)
		require.Contains(t, defaultValue, "'{}'::jsonb")
	}
}
