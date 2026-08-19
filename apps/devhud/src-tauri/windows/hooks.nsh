!macro NSIS_HOOK_PREUNINSTALL
  IfFileExists "$INSTDIR\devhud-native-messaging-host.exe" 0 +2
    nsExec::ExecToLog '"$INSTDIR\devhud-native-messaging-host.exe" unregister'
!macroend
