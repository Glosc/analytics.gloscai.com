using FluentMigrator;

namespace Aptabase.Data.Migrations;

// Replaces the e-mail magic-link login with username + password accounts.
// `users.username` is the account identifier, `password_hash` is a PBKDF2 digest
// (null for OAuth-only accounts), and `role` carries the single `admin` role that
// the first registered account receives.
[Migration(0015)]
public class UsernamePasswordAuth : Migration
{
    public override void Up()
    {
        Alter.Table("users")
            .AddColumn("username").AsString(40).Nullable()
            .AddColumn("password_hash").AsString(500).Nullable()
            .AddColumn("role").AsString(20).NotNullable().WithDefaultValue("user");

        // Accounts created before password auth get a deterministic username derived from
        // their e-mail. The id suffix keeps it unique; these accounts have no password and
        // can only sign in through OAuth until one is set.
        Execute.Sql(@"
            UPDATE users
            SET username = left(coalesce(nullif(regexp_replace(split_part(lower(email), '@', 1), '[^a-z0-9._-]', '', 'g'), ''), 'user'), 17)
                        || '_' || lower(left(id, 22))
            WHERE username IS NULL;");

        Alter.Column("username").OnTable("users").AsString(40).NotNullable();
        Alter.Column("email").OnTable("users").AsString(300).Nullable();

        Create.Index("idx_users_username")
              .OnTable("users")
              .OnColumn("username").Ascending()
              .WithOptions().Unique();

        // Only one account may hold the admin role at a time; this also makes the
        // "first registration becomes admin" insert race-safe.
        Execute.Sql("CREATE UNIQUE INDEX idx_users_single_admin ON users (role) WHERE role = 'admin';");

        // Single-use tracking for magic-link tokens; the links no longer exist.
        Delete.Table("auth_token_uses");
    }

    public override void Down()
    {
        Execute.Sql("UPDATE users SET email = '' WHERE email IS NULL;");

        Create.Table("auth_token_uses")
            .WithNanoIdColumn("token_id").PrimaryKey()
            .WithColumn("expires_at").AsDateTime().NotNullable()
            .WithColumn("used_at").AsDateTime().NotNullable();

        Create.Index("idx_auth_token_uses_expires_at")
            .OnTable("auth_token_uses")
            .OnColumn("expires_at");

        Execute.Sql("DROP INDEX IF EXISTS idx_users_single_admin;");
        Delete.Index("idx_users_username").OnTable("users");

        Alter.Column("email").OnTable("users").AsString(300).NotNullable();
        Delete.Column("role").FromTable("users");
        Delete.Column("password_hash").FromTable("users");
        Delete.Column("username").FromTable("users");
    }
}
