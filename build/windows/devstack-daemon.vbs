Set WshShell = CreateObject("WScript.Shell")
WshShell.Run """C:\devstack\bin\devstack.exe"" daemon run", 0, False
