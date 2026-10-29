Set WshShell = CreateObject("WScript.Shell")
userProf = WshShell.ExpandEnvironmentStrings("%USERPROFILE%")
WshShell.Run """" & userProf & "\.devstack\bin\devstack.exe"" daemon run", 0, False
