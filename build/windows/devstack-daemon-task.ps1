# Register the devstack daemon to start at user logon without elevation.
# Run from a normal PowerShell prompt; it never elevates.
param(
    [string]$Home = "$env:USERPROFILE\.devstack",
    [string]$Binary = "$env:LOCALAPPDATA\devstack\devstack.exe"
)

$ErrorActionPreference = "Stop"
$taskName = "devstack-daemon"
$action = New-ScheduledTaskAction -Execute $Binary -Argument "daemon run" -WorkingDirectory $Home
$trigger = New-ScheduledTaskTrigger -AtLogOn
$principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" -LogonType Interactive -RunLevel Limited
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)
Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
Write-Output "registered $taskName for limited user $env:USERNAME"
