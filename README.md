<div align="center">
  <a href="https://github.com/aptabase/aptabase">
    <img src="https://aptabase.com/og.png" alt="Aptabase"/>
  </a>

  <h3 align="center">Aptabase</h3>

  <p align="center">
    Analytics for Apps.
    <br />
    Open Source. Privacy-First. Simple.
    <br />
    <br />
    <a href="https://aptabase.com"><strong>Learn more »</strong></a>
  </p>
</div>

# About the Project

[Aptabase](https://aptabase.com) is an open-source alternative to Firebase/Google Analytics, specifically built for Mobile, Desktop and Web apps.

📱 **Extensive list of SDK**: No matter what framework or language you use, we have an SDK for you. Swift, React Native, Flutter, Electron, Kotlin, and many others.

😇 **Privacy-First**: We prioritize user privacy and collect minimal usage data without using unique identifiers. Instead, we focus on monitoring sessions, complying fully with GDPR, CCPA, and PECR regulations.

🚀 **Simple**: Built-in and user-friendly dashboard for all your essential metrics, enabling you to gain insights effortlessly and grasp the dynamics of your apps.

💯 **Open-Source**: Our source code is 100% open source. There is nothing hidden. All the server code and SDKs are available for you to inspect and contribute to.

> [!TIP]
> Like what you see? Don't forget to give us a ⭐️ on GitHub!

# How to get started?

## ☁️ Managed Cloud

The easiest and quickest way to get started. A fully managed service by the creators of Aptabase to help you get started in minutes. Forget about managing software updates and patches; we do it all for you!

[Try free now →](https://aptabase.com)

## 🏢 Self-Hosted

You can also host Aptabase on your servers. It's free, but you are responsible for maintenance and updates.

This repository's production Compose file runs the prebuilt image from
`ghcr.io/glosc/analytics.gloscai.com` with PostgreSQL and ClickHouse. From the repository root:

```bash
cp .env.example .env
chmod 600 .env
```

Set `BASE_URL` to your public HTTPS URL. Generate `AUTH_SECRET` with `openssl rand -base64 32`
and generate each database password separately with `openssl rand -hex 32`. Keep `.env` private
and retain the same secrets across upgrades. The database passwords are used in connection
strings. The generated hex values avoid connection-string delimiters.

Users register and sign in with a username and password. SMTP is optional. With `SMTP_HOST`
unset, the app logs notifications. To send email, set `SMTP_HOST`
and `SMTP_FROM_ADDRESS`, plus `SMTP_USERNAME` and `SMTP_PASSWORD` if your server requires them.

```bash
docker compose pull
docker compose up -d
docker compose logs -f aptabase
```

Point an HTTPS reverse proxy on the host at `http://127.0.0.1:8080` (or the port set by
`APTABASE_PORT`). Have the proxy supply the original host and scheme and set the client IP
headers itself. Verify the app responds at `/healthz`. Both databases use the private Compose
network.

Publishing a GitHub Release builds the `linux/amd64` and `linux/arm64` images and pushes them
under the Release tag (for example, `v1.2.3`). The release marked **Latest** on GitHub also
gets the `latest` image tag. A prerelease can be pulled by its version tag.

To pin a version, set `APTABASE_IMAGE_TAG` in `.env` to the Release tag. Back up both databases
before upgrading, then run `docker compose pull` and `docker compose up -d`. The app runs
database migrations on startup.

PostgreSQL, ClickHouse, and the key ring from earlier releases use named volumes
under the `aptabase-production` Compose project. Keep that project name and its volumes
when upgrading. The Go server reads existing login cookies using the original key ring
and renews them using `AUTH_SECRET`. Keep this secret stable across restarts and replicas.
Users who started an OAuth sign-in before the upgrade can restart it. If you roll back to
the .NET release, users with Go-issued sessions need to sign in again. Running
`docker compose down -v` deletes the volumes.

[Upstream self-hosting guide →](https://github.com/aptabase/self-hosting).

## 🛠️ Contributing

We welcome contributions from everyone! If you want to contribute to Aptabase, please read the guide below to get started.

[Contributing →](./CONTRIBUTING.md).

# SDKs

We provide SDKs for the most popular frameworks and languages to make it easier to integrate them into your app.

- [Swift (Apple)](https://github.com/aptabase/aptabase-swift)
- [Android (Kotlin)](https://github.com/aptabase/aptabase-kotlin)
- [React Native](https://github.com/aptabase/aptabase-react-native)
- [Flutter](https://github.com/aptabase/aptabase_flutter)
- [Tauri](https://github.com/aptabase/tauri-plugin-aptabase)
- [NativeScript](https://github.com/nstudio/nativescript-plugins/tree/main/packages/nativescript-aptabase)
- [.NET MAUI](https://github.com/aptabase/aptabase-maui)
- [Electron](https://github.com/aptabase/aptabase-electron)
- [Web Apps](https://github.com/aptabase/aptabase-js)
- [Unreal Engine](https://github.com/aptabase/aptabase-unreal)
- [Unity Engine](https://github.com/aptabase/aptabase-unity)

🛠️ Don't see the SDK for your platform? You can build your own! Check out our guide on [how to build your own SDK](https://github.com/aptabase/aptabase/wiki/How-to-build-your-own-SDK).

# Need help?

- Find us on [Discord](https://discord.gg/d9d97unCUk) or [Twitter](https://twitter.com/aptabase)
- Open a GitHub issue or a discussion on this repo
- Shoot us an email [hi@aptabase.com](mailto:hi@aptabase.com)

# Credits

Aptabase is highly inspired by [Plausible](https://github.com/plausible/analytics). If you're looking for privacy-friendly website analytics, go check them out. They're awesome! ❤️

# License

Aptabase is open-source under the [AGPLv3 license](./LICENSE). You can use it for free, but you must share any changes you make to the code.

The SDKs are open-source under the MIT license. Use it on your apps without any restrictions.
