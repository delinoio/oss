# TaiRouter

TaiRouter stands for **Team AI Router**. It is an open-source Delino Labs product and a self-hosted AI provider router for organizations. Each member uses their own provider credentials, account pools, and routing. Those resources are separate in every organization, even for the same person.

## Read this before deploying

These guides describe the required product behavior and the available setup interface. They are not a release-readiness statement. Workflows without a verified executable procedure are marked unavailable. Do not treat a documentation build, a compiled binary, or a mock test as proof of a working provider account or production deployment.

Start with [installation](guide/installation.md) and [service configuration](guide/configuration.md). Read [upgrade and recovery](guide/recovery.md) before storing data. Check [compatibility](guide/compatibility.md) for verification limits.

Visit the [TaiRouter product page](https://labs.delino.io/tairouter/) for an introduction, or [view the source on GitHub](https://github.com/delinoio/oss).

## First connection

The console guides you through this order:

1. Sign in through your operator's identity provider.
2. Create an organization or accept an invitation. A personal organization is not created automatically.
3. Select that organization and connect your own providers.
4. Configure models and routing.
5. Issue an API key or approve a dedicated client connection.
6. Test the selected client and model with non-sensitive input.

See [organizations](guide/organizations.md), [providers](guide/providers.md), and [clients](guide/clients.md).

## Console language

Use the language selector at the top right of the console, including the sign-in page. Language options are English, Korean, Japanese, Simplified Chinese, Spanish, French, German, and Brazilian Portuguese. English is the default. Translations have not been generated yet, so selecting another language currently keeps the console text in English. Your choice applies to this browser and does not change the selected organization or page. Dates and numbers follow the selected language; displayed timezones remain explicit. These TaiRouter guides are published in English. The TaiRouter links in each localized Labs sidebar open these English guides.

## Product boundaries

TaiRouter is not a hosted SaaS, billing service, or remote command executor. It does not provide shared organization credentials, automatic import, user data export, or permanent conversation or media storage. Configuring a client and using an HTTP loopback relay are distinct from executing that client as a provider backend; the latter is excluded.

Repository-owned TaiRouter software is licensed under Apache-2.0. Imported components retain their own licenses and notices.
