# Commit. Keep moving.

ach runs checks against an isolated copy of your committed source. Durable receipts help you and your coding agent follow results while work continues. Results and the web UI are served directly by ach on your computer.

[Install ach](./install), then follow [Get started](./start) to register a trusted repository and configure its checks.

```sh
ach init
ach ui
```

Open the local URL printed by `ach ui`. No pairing code, account, or separate web server is required. The default address is `http://127.0.0.1:46309/`; your personal `api_port` setting determines the port. In on-demand mode, keep `ach ui` running while viewing results.

This website provides documentation and verified installer entrypoints. It never connects to your local ach server or receives your results. See [Browser connection](./web), [Final validation](./validation), and [Privacy and trust](./privacy).
