Set WshShell = CreateObject("WScript.Shell")
userProf = WshShell.ExpandEnvironmentStrings("%USERPROFILE%")
homeDir = WshShell.ExpandEnvironmentStrings("%DEVSTACK_HOME%")
If homeDir = "%DEVSTACK_HOME%" Then
    homeDir = userProf & "\.devstack"
End If
WshShell.Run """" & homeDir & "\bin\devstack.exe"" daemon run --home """ & homeDir & """", 0, False
