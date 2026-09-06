import { desktopNativeMessagingIntegration } from "./native-messaging-ui";
import { desktopSettingsIntegration } from "./desktop-settings-ui";
import { renderApp } from "./main";

renderApp({ nativeMessaging: desktopNativeMessagingIntegration, desktopSettings: desktopSettingsIntegration });
