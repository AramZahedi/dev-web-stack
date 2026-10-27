# Register the devstack daemon to start at user logon without elevation.
# Run from a normal PowerShell prompt; it never elevates.
param(
    [string]$Home = "$env:USERPROFILE\.devstack",
    [string]$Binary = "C:\devstack\bin\devstack.exe",
    [string]$UserId = "$env:USERDOMAIN\$env:USERNAME"
)

$ErrorActionPreference = "Stop"
$taskName = "devstack-daemon"
if (-not (Test-Path $Home)) {
    New-Item -ItemType Directory -Force -Path $Home | Out-Null
}
$action = New-ScheduledTaskAction -Execute $Binary -Argument "daemon run" -WorkingDirectory $Home
$trigger = try {
    New-ScheduledTaskTrigger -AtLogOn -User $UserId
} catch {
    New-ScheduledTaskTrigger -AtLogOn
}
$principal = New-ScheduledTaskPrincipal -UserId $UserId -LogonType Interactive -RunLevel Limited
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)
Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
Write-Output "registered $taskName for limited user $env:USERNAME"
