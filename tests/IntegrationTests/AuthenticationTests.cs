using System.Net;
using System.Net.Http.Json;
using Aptabase.IntegrationTests.Clients;
using Xunit;
using AwesomeAssertions;

namespace Aptabase.IntegrationTests;

[Collection("Integration Tests")]
public class AuthenticationTests
{
    private readonly IntegrationTestsFixture _fixture;

    public AuthenticationTests(IntegrationTestsFixture fixture)
    {
        _fixture = fixture;
    }

    private class MeResponse
    {
        public string Id { get; set; } = "";
        public string Name { get; set; } = "";
        public string Username { get; set; } = "";
        public string Role { get; set; } = "";
    }

    [Fact]
    public async Task Registering_A_New_Account_Signs_It_In()
    {
        var client = new AccountClient(_fixture.CreateClient());
        var username = await client.CreateAccount("New User");

        var response = await client.GetMe();
        response.StatusCode.Should().Be(HttpStatusCode.OK);

        var me = await response.Content.ReadFromJsonAsync<MeResponse>();
        me!.Username.Should().Be(username);
        me.Role.Should().BeOneOf("admin", "user");
    }

    [Fact]
    public async Task Registering_With_A_Taken_Username_Fails()
    {
        var username = await new AccountClient(_fixture.CreateClient()).CreateAccount("First Owner");

        var response = await new AccountClient(_fixture.CreateClient())
            .Register("Second Owner", username, "AnotherPassword123!");

        response.StatusCode.Should().Be(HttpStatusCode.Conflict);
    }

    [Fact]
    public async Task Signing_In_With_The_Wrong_Password_Fails()
    {
        var username = await new AccountClient(_fixture.CreateClient())
            .CreateAccount("Wrong Password", password: "CorrectPassword123!");

        var response = await new AccountClient(_fixture.CreateClient())
            .Login(username, "WrongPassword123!");

        response.StatusCode.Should().Be(HttpStatusCode.Unauthorized);
    }

    [Fact]
    public async Task Signing_In_With_The_Right_Password_Succeeds()
    {
        var username = await new AccountClient(_fixture.CreateClient())
            .CreateAccount("Right Password", password: "MyPassword123!");

        var client = new AccountClient(_fixture.CreateClient());
        (await client.Login(username, "MyPassword123!")).StatusCode.Should().Be(HttpStatusCode.OK);
        (await client.GetMe()).StatusCode.Should().Be(HttpStatusCode.OK);
    }
}
