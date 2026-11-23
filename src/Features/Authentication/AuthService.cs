using Aptabase.Data;
using Microsoft.AspNetCore.Authentication;
using Microsoft.AspNetCore.Authentication.Cookies;
using Npgsql;
using System.Security.Claims;
using Dapper;

namespace Aptabase.Features.Authentication;

public enum RegisterResult
{
    Success,
    UsernameTaken
}

public interface IAuthService
{
    Task<(RegisterResult Result, UserAccount? User)> RegisterAsync(string name, string username, string password, CancellationToken cancellationToken);
    Task<UserAccount?> SignInWithPasswordAsync(string username, string password, CancellationToken cancellationToken);
    Task SignInAsync(UserAccount user);
    Task SignOutAsync();
    Task<UserAccount?> FindUserByIdAsync(string id, CancellationToken cancellationToken);
    Task<UserAccount?> FindUserByUsernameAsync(string username, CancellationToken cancellationToken);
    Task<UserAccount?> FindUserByEmailAsync(string email, CancellationToken cancellationToken);
    Task<UserAccount?> FindUserByOAuthProviderAsync(string providerName, string providerUid, CancellationToken cancellationToken);
    Task<UserAccount> FindOrCreateAccountWithOAuthAsync(string name, string email, string providerName, string providerUid, CancellationToken cancellationToken);
    Task AttachUserAuthProviderAsync(UserAccount user, string providerName, string providerUid, CancellationToken cancellationToken);
    Task DeleteUserByIdAsync(string id, CancellationToken cancellationToken);
}

public class AuthService : IAuthService
{
    // A password hash that never matches, used to keep failed sign-ins on par time-wise
    // with successful ones when the username does not exist.
    private static readonly string DummyPasswordHash = PasswordHasher.Hash("a-password-that-is-never-correct");

    private const string SingleAdminIndex = "idx_users_single_admin";

    private readonly ILogger _logger;
    private readonly IDbContext _db;
    private readonly IHttpContextAccessor _httpContextAccessor;

    public AuthService(
        ILogger<AuthService> logger,
        IDbContext db,
        IHttpContextAccessor httpContextAccessor)
    {
        _logger = logger ?? throw new ArgumentNullException(nameof(logger));
        _httpContextAccessor = httpContextAccessor ?? throw new ArgumentNullException(nameof(httpContextAccessor));
        _db = db ?? throw new ArgumentNullException(nameof(db));
    }

    public async Task<(RegisterResult Result, UserAccount? User)> RegisterAsync(string name, string username, string password, CancellationToken cancellationToken)
    {
        var normalized = NormalizeUsername(username);
        var displayName = string.IsNullOrWhiteSpace(name) ? normalized : name.Trim();
        var passwordHash = PasswordHasher.Hash(password);

        var (user, usernameTaken) = await InsertAccountAsync(NanoId.New(), displayName, normalized, null, passwordHash, cancellationToken);
        if (user is null)
            return (usernameTaken ? RegisterResult.UsernameTaken : RegisterResult.UsernameTaken, null);

        return (RegisterResult.Success, user);
    }

    public async Task<UserAccount?> SignInWithPasswordAsync(string username, string password, CancellationToken cancellationToken)
    {
        var cmd = new CommandDefinition(@"
            SELECT id, name, username, coalesce(email, '') AS email, role, password_hash, lock_reason
            FROM users
            WHERE username = @username",
            new { username = NormalizeUsername(username) },
            cancellationToken: cancellationToken
        );

        var row = await _db.Connection.QuerySingleOrDefaultAsync<UserWithPassword>(cmd);
        if (row is null)
        {
            // Spend the same work on a missing account as on a wrong password.
            PasswordHasher.Verify(password, DummyPasswordHash);
            return null;
        }

        if (!PasswordHasher.Verify(password, row.PasswordHash))
            return null;

        return row.ToUserAccount();
    }

    public async Task SignOutAsync()
    {
        if (_httpContextAccessor.HttpContext == null)
            return;

        await _httpContextAccessor.HttpContext.SignOutAsync(CookieAuthenticationDefaults.AuthenticationScheme);
    }

    public async Task SignInAsync(UserAccount user)
    {
        if (_httpContextAccessor.HttpContext == null)
        {
            _logger.LogError("Tried to authenticate without an active HTTP Context");
            return;
        }

        var claims = new List<Claim>
        {
            new("id", user.Id),
            new("name", user.Name),
            new("username", user.Username),
            new("email", user.Email),
            new("role", user.Role),
        };

        var claimsIdentity = new ClaimsIdentity(
            claims, CookieAuthenticationDefaults.AuthenticationScheme);

        var authProperties = new AuthenticationProperties
        {
            ExpiresUtc = DateTime.UtcNow.AddDays(365),
            IsPersistent = true,
        };

        await _httpContextAccessor.HttpContext.SignInAsync(
            CookieAuthenticationDefaults.AuthenticationScheme,
            new ClaimsPrincipal(claimsIdentity),
            authProperties);
    }

    public Task DeleteUserByIdAsync(string id, CancellationToken cancellationToken)
    {
        var sql = @"
        BEGIN;
            DELETE FROM public.app_shares 
            WHERE app_id IN (SELECT id FROM public.apps WHERE owner_id = @userId);

            DELETE FROM public.app_salts 
            WHERE app_id IN (SELECT id FROM public.apps WHERE owner_id = @userId);

            DELETE FROM public.apps 
            WHERE owner_id = @userId;

            DELETE FROM public.user_providers 
            WHERE user_id = @userId;

            DELETE FROM public.subscriptions 
            WHERE owner_id = @userId;

            DELETE FROM public.users 
            WHERE id = @userId;
        COMMIT;";

        var cmd = new CommandDefinition(
            sql,
            new { userId = id },
            cancellationToken: cancellationToken
        );

        return _db.Connection.ExecuteAsync(cmd);
    }

    public async Task<UserAccount?> FindUserByIdAsync(string id, CancellationToken cancellationToken)
    {
        var cmd = new CommandDefinition(@"
            SELECT id, name, username, coalesce(email, '') AS email, role, lock_reason
            FROM users WHERE id = @id",
            new { id }, cancellationToken: cancellationToken);
        return await _db.Connection.QuerySingleOrDefaultAsync<UserAccount>(cmd);
    }

    public async Task<UserAccount?> FindUserByUsernameAsync(string username, CancellationToken cancellationToken)
    {
        var cmd = new CommandDefinition(@"
            SELECT id, name, username, coalesce(email, '') AS email, role, lock_reason
            FROM users WHERE username = @username",
            new { username = NormalizeUsername(username) }, cancellationToken: cancellationToken);
        return await _db.Connection.QuerySingleOrDefaultAsync<UserAccount>(cmd);
    }

    public async Task<UserAccount?> FindUserByEmailAsync(string email, CancellationToken cancellationToken)
    {
        var cmd = new CommandDefinition(@"
            SELECT id, name, username, coalesce(email, '') AS email, role, lock_reason
            FROM users WHERE email = @email",
            new { email = email.ToLower() }, cancellationToken: cancellationToken);
        return await _db.Connection.QuerySingleOrDefaultAsync<UserAccount>(cmd);
    }

    public async Task<UserAccount> FindOrCreateAccountWithOAuthAsync(string name, string email, string providerName, string providerUid, CancellationToken cancellationToken)
    {
        var user = await FindUserByOAuthProviderAsync(providerName, providerUid, cancellationToken);
        if (user is not null)
            return user;

        user = await FindUserByEmailAsync(email, cancellationToken);
        if (user is not null)
        {
            await AttachUserAuthProviderAsync(user, providerName, providerUid, cancellationToken);
            return user;
        }

        user = await CreateOAuthAccountAsync(name, email, cancellationToken);
        await AttachUserAuthProviderAsync(user, providerName, providerUid, cancellationToken);
        return user;
    }

    public async Task<UserAccount?> FindUserByOAuthProviderAsync(string providerName, string providerUid, CancellationToken cancellationToken)
    {
        var cmd = new CommandDefinition($@"
            SELECT u.id, u.name, u.username, coalesce(u.email, '') AS email, u.role, u.lock_reason
            FROM user_providers up
            INNER JOIN users u
            ON u.id = up.user_id
            WHERE up.provider_name = @name
            AND up.provider_uid = @uid", new { name = providerName, uid = providerUid }, cancellationToken: cancellationToken);
        return await _db.Connection.QuerySingleOrDefaultAsync<UserAccount>(cmd);
    }

    public Task AttachUserAuthProviderAsync(UserAccount user, string providerName, string providerUid, CancellationToken cancellationToken)
    {
        var cmd = new CommandDefinition($@"
            INSERT INTO user_providers (provider_name, provider_uid, user_id)
            VALUES (@providerName, @providerUid, @userId)", new { userId = user.Id, providerName, providerUid }, cancellationToken: cancellationToken);
        return _db.Connection.ExecuteAsync(cmd);
    }

    private async Task<(UserAccount? User, bool UsernameTaken)> InsertAccountAsync(string id, string name, string username, string? email, string? passwordHash, CancellationToken cancellationToken)
    {
        // The admin slot is decided inside the INSERT so that two accounts registering at the
        // same time cannot both become admin; the partial unique index on role is the backstop.
        const string sql = @"
            INSERT INTO users (id, name, username, email, password_hash, role, free_quota)
            SELECT @id, @name, @username, @email, @passwordHash,
                   CASE
                       WHEN @forceRole <> '' THEN @forceRole
                       WHEN EXISTS (SELECT 1 FROM users WHERE role = 'admin') THEN 'user'
                       ELSE 'admin'
                   END,
                   200000000
            RETURNING id, name, username, coalesce(email, '') AS email, role, lock_reason";

        try
        {
            var user = await _db.Connection.QuerySingleAsync<UserAccount>(new CommandDefinition(
                sql,
                new { id, name, username, email = string.IsNullOrWhiteSpace(email) ? null : email.ToLower(), passwordHash, forceRole = "" },
                cancellationToken: cancellationToken
            ));
            return (user, false);
        }
        catch (PostgresException ex) when (ex.SqlState == PostgresErrorCodes.UniqueViolation && ex.ConstraintName == SingleAdminIndex)
        {
            // Another registration took the admin slot first; this account is a regular user.
            try
            {
                var user = await _db.Connection.QuerySingleAsync<UserAccount>(new CommandDefinition(
                    sql,
                    new { id, name, username, email = string.IsNullOrWhiteSpace(email) ? null : email.ToLower(), passwordHash, forceRole = UserRoles.User },
                    cancellationToken: cancellationToken
                ));
                return (user, false);
            }
            catch (PostgresException retryEx) when (retryEx.SqlState == PostgresErrorCodes.UniqueViolation)
            {
                return (null, true);
            }
        }
        catch (PostgresException ex) when (ex.SqlState == PostgresErrorCodes.UniqueViolation)
        {
            return (null, true);
        }
    }

    private async Task<UserAccount> CreateOAuthAccountAsync(string name, string email, CancellationToken cancellationToken)
    {
        var baseUsername = BuildUsernameFromEmail(email);

        for (var attempt = 0; attempt < 5; attempt++)
        {
            var username = attempt == 0 ? baseUsername : $"{baseUsername}_{NanoId.New()[..6].ToLowerInvariant()}";
            var (user, usernameTaken) = await InsertAccountAsync(NanoId.New(), name, username, email, null, cancellationToken);
            if (user is not null)
                return user;
            if (!usernameTaken)
                break;
        }

        throw new InvalidOperationException($"Unable to allocate a username for {email}");
    }

    private static string NormalizeUsername(string username) => username.Trim().ToLowerInvariant();

    private static string BuildUsernameFromEmail(string email)
    {
        var local = email.Split('@')[0];
        var cleaned = new string(local.Where(c => char.IsAsciiLetterOrDigit(c) || c is '.' or '_' or '-').ToArray()).ToLowerInvariant();
        if (cleaned.Length < 3)
            cleaned = "user";

        return cleaned[..Math.Min(30, cleaned.Length)];
    }

    private class UserWithPassword
    {
        public string Id { get; set; } = "";
        public string Name { get; set; } = "";
        public string Username { get; set; } = "";
        public string Email { get; set; } = "";
        public string Role { get; set; } = UserRoles.User;
        public string? PasswordHash { get; set; }
        public char? LockReason { get; set; }

        public UserAccount ToUserAccount() => new()
        {
            Id = Id,
            Name = Name,
            Username = Username,
            Email = Email,
            Role = Role,
            LockReason = LockReason,
        };
    }
}
