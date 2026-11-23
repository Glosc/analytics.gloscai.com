using System.ComponentModel.DataAnnotations;
using Microsoft.AspNetCore.Authentication;
using Microsoft.AspNetCore.Cors;
using Microsoft.AspNetCore.Mvc;
using Microsoft.AspNetCore.RateLimiting;

namespace Aptabase.Features.Authentication;

public class LoginBodyRequest
{
    [Required]
    public string Username { get; set; } = "";

    [Required]
    public string Password { get; set; } = "";
}

public class RegisterBodyRequest
{
    [StringLength(40, MinimumLength = 2)]
    public string Name { get; set; } = "";

    [Required]
    [StringLength(40, MinimumLength = 3, ErrorMessage = "Username must be between 3 and 40 characters.")]
    [RegularExpression("^[a-zA-Z0-9._-]+$", ErrorMessage = "Username may only contain letters, numbers, dots, dashes and underscores.")]
    public string Username { get; set; } = "";

    [Required]
    [StringLength(PasswordHasher.MaxPasswordLength, MinimumLength = PasswordHasher.MinPasswordLength, ErrorMessage = "Password must be at least 8 characters.")]
    public string Password { get; set; } = "";
}

[ApiController]
[ResponseCache(NoStore = true, Location = ResponseCacheLocation.None)]
public class AuthController : Controller
{
    private readonly ILogger _logger;
    private readonly EnvSettings _env;
    private readonly IAuthService _authService;

    public AuthController(
        ILogger<AuthController> logger,
        EnvSettings env,
        IAuthService authService)
    {
        _env = env ?? throw new ArgumentNullException(nameof(env));
        _logger = logger ?? throw new ArgumentNullException(nameof(logger));
        _authService = authService ?? throw new ArgumentNullException(nameof(authService));
    }

    [HttpPost("/api/_auth/register")]
    [EnableRateLimiting("SignUp")]
    public async Task<IActionResult> Register([FromBody] RegisterBodyRequest body, CancellationToken cancellationToken)
    {
        var (result, user) = await _authService.RegisterAsync(body.Name?.Trim() ?? "", body.Username, body.Password, cancellationToken);

        if (result == RegisterResult.UsernameTaken || user is null)
            return Conflict(new { error = "username_taken" });

        await _authService.SignInAsync(user);
        _logger.LogInformation("Registered account {username} (role {role})", user.Username, user.Role);

        return Ok(user);
    }

    [HttpPost("/api/_auth/login")]
    [EnableRateLimiting("SignIn")]
    public async Task<IActionResult> Login([FromBody] LoginBodyRequest body, CancellationToken cancellationToken)
    {
        var user = await _authService.SignInWithPasswordAsync(body.Username, body.Password, cancellationToken);
        if (user is null)
            return Unauthorized(new { error = "invalid_credentials" });

        await _authService.SignInAsync(user);
        return Ok(user);
    }

    [HttpGet("/api/_auth/github")]
    public IActionResult GitHub()
    {
        if (string.IsNullOrWhiteSpace(_env.OAuthGitHubClientId) || string.IsNullOrWhiteSpace(_env.OAuthGitHubClientSecret))
            return NotFound(new { });

        return Challenge(new AuthenticationProperties { RedirectUri = $"{_env.SelfBaseUrl}/" }, "github");
    }

    [HttpGet("/api/_auth/google")]
    public IActionResult Google()
    {
        if (string.IsNullOrWhiteSpace(_env.OAuthGoogleClientId) || string.IsNullOrWhiteSpace(_env.OAuthGoogleClientSecret))
            return NotFound(new { });

        return Challenge(new AuthenticationProperties { RedirectUri = $"{_env.SelfBaseUrl}/" }, "google");
    }

    [HttpGet("/api/_auth/providers")]
    public IActionResult Providers()
    {
        return Ok(new
        {
            github = !string.IsNullOrWhiteSpace(_env.OAuthGitHubClientId) && !string.IsNullOrWhiteSpace(_env.OAuthGitHubClientSecret),
            google = !string.IsNullOrWhiteSpace(_env.OAuthGoogleClientId) && !string.IsNullOrWhiteSpace(_env.OAuthGoogleClientSecret)
        });
    }

    [HttpGet("/api/_auth/me")]
    [IsAuthenticated]
    [EnableCors("AllowAptabaseCom")]
    public async Task<IActionResult> Me(CancellationToken cancellationToken)
    {
        var identity = this.GetCurrentUserIdentity();
        var user = await _authService.FindUserByIdAsync(identity.Id, cancellationToken);
        if (user is null)
            return NotFound();

        return Ok(user);
    }

    [HttpPost("/api/_auth/account/delete")]
    [IsAuthenticated]
    [EnableCors("AllowAptabaseCom")]
    public async Task<IActionResult> DeleteAccount(CancellationToken cancellationToken)
    {
        var identity = this.GetCurrentUserIdentity();
        var user = await _authService.FindUserByIdAsync(identity.Id, cancellationToken);
        if (user is null)
            return NotFound();

        await _authService.DeleteUserByIdAsync(identity.Id, cancellationToken);

        return Ok(user);
    }

    [HttpPost("/api/_auth/signout")]
    public async Task<IActionResult> ForceSignOut()
    {
        await _authService.SignOutAsync();
        return Redirect($"{_env.SelfBaseUrl}/auth");
    }
}
