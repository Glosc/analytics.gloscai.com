using FluentMigrator;

namespace Aptabase.Data.Migrations;

// Raises the monthly event quota of every free account from the old 20k default
// to 200 million. New accounts get the same quota at signup (see AuthService).
[Migration(0016)]
public class RaiseFreeQuotaTo200M : Migration
{
    public override void Up()
    {
        Execute.Sql("UPDATE users SET free_quota = 200000000 WHERE free_quota = 20000;");
    }

    public override void Down()
    {
        Execute.Sql("UPDATE users SET free_quota = 20000 WHERE free_quota = 200000000;");
    }
}
