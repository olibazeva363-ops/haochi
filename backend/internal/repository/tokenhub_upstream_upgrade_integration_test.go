//go:build integration

package repository

import (
	"context"
	"database/sql"
	_ "embed"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Frozen from the last local checkpoint, be143e40b, before integrating v0.2.1.
// Those SQL files are byte-for-byte unchanged in the upstream release.
//
//go:embed testdata/tokenhub_pre_021_migrations.txt
var tokenHubPre021MigrationManifest string

func TestTokenHubUpgradeFromPre021PreservesDataAndIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pg, err := tcpostgres.Run(ctx, selectDockerImage(ctx, postgresImageTag),
		tcpostgres.WithDatabase("tokenhub_upgrade_test"),
		tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	baseline := fstest.MapFS{}
	for _, name := range strings.Fields(tokenHubPre021MigrationManifest) {
		body, readErr := migrations.FS.ReadFile(name)
		require.NoError(t, readErr)
		baseline[name] = &fstest.MapFile{Data: body, Mode: 0444}
	}
	require.Len(t, baseline, 244)
	require.NoError(t, applyMigrationsFS(ctx, db, baseline))

	// Exercise data that is affected by the new migrations, alongside balances,
	// custom settings and credentials that must survive an upgrade unchanged.
	_, err = db.ExecContext(ctx, `
INSERT INTO users (email, password_hash, balance) VALUES ('upgrade@example.test', 'test-only', 12.34567890);
INSERT INTO settings (key, value) VALUES ('site_name', 'TokenHub preserved') ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;
INSERT INTO accounts (name, platform, type, credentials, extra) VALUES
 ('upgrade-sk', 'anthropic', 'oauth', '{"claude_sk":"test-fixture"}', '{"claude_frozen_environment_profile":{"fixture":true}}'),
 ('upgrade-codex', 'openai', 'oauth', '{}', '{"codex_fingerprint_mode":"session"}');
INSERT INTO groups (name, platform, video_price_480p) VALUES
 ('upgrade-claude', 'anthropic', 0.25), ('upgrade-grok', 'grok', 0.50), ('upgrade-composite', 'composite', 0.75);
`)
	require.NoError(t, err)
	require.NoError(t, ApplyMigrations(ctx, db))
	require.NoError(t, ApplyMigrations(ctx, db), "second startup must not replay data changes")

	var balance, siteName, sk, seed string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT balance::text FROM users WHERE email = 'upgrade@example.test'").Scan(&balance))
	require.Equal(t, "12.34567890", balance)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = 'site_name'").Scan(&siteName))
	require.Equal(t, "TokenHub preserved", siteName)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT credentials->>'claude_sk' FROM accounts WHERE name = 'upgrade-sk'").Scan(&sk))
	require.Equal(t, "test-fixture", sk)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT extra->>'codex_fingerprint_seed' FROM accounts WHERE name = 'upgrade-codex'").Scan(&seed))
	requireCanonicalUUIDString(t, seed)

	var cleared bool
	require.NoError(t, db.QueryRowContext(ctx, "SELECT video_price_480p IS NULL FROM groups WHERE name = 'upgrade-claude'").Scan(&cleared))
	require.True(t, cleared)
	var backupPrice, grokPrice, compositePrice float64
	require.NoError(t, db.QueryRowContext(ctx, "SELECT b.video_price_480p FROM groups_video_price_backup_220 b JOIN groups g ON g.id=b.group_id WHERE g.name='upgrade-claude'").Scan(&backupPrice))
	require.Equal(t, 0.25, backupPrice)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT video_price_480p FROM groups WHERE name='upgrade-grok'").Scan(&grokPrice))
	require.Equal(t, 0.50, grokPrice)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT video_price_480p FROM groups WHERE name='upgrade-composite'").Scan(&compositePrice))
	require.Equal(t, 0.75, compositePrice)

	var mode string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='channel_monitor_mode'").Scan(&mode))
	require.Equal(t, "v1", mode, "upgrade must preserve the opt-in monitor mode")
	files, err := fs.Glob(migrations.FS, "*.sql")
	require.NoError(t, err)
	var applied int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&applied))
	require.Equal(t, len(files), applied)
}
