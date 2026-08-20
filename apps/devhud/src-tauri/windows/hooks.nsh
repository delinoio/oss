!macro NSIS_HOOK_PREUNINSTALL
  IfFileExists "$INSTDIR\devhud-native-messaging-host.exe" devhud_native_messaging_unregister devhud_native_messaging_unregister_missing
  devhud_native_messaging_unregister:
    nsExec::ExecToLog '"$INSTDIR\devhud-native-messaging-host.exe" unregister'
    Pop $0
    StrCmp $0 "0" devhud_native_messaging_unregister_done
    Abort "DevHud Native Messaging cleanup failed. Close DevHud and retry the uninstall."
  devhud_native_messaging_unregister_missing:
    Abort "DevHud Native Messaging cleanup executable is missing. Repair or reinstall DevHud before uninstalling."
  devhud_native_messaging_unregister_done:
!macroend
