# Register the devstack daemon to start at user logon without elevation.
# Run from a normal PowerShell prompt; it never elevates.
param(
    [string]$HomeDir = "$env:USERPROFILE\.devstack",
    [string]$Binary = "$env:USERPROFILE\.devstack\bin\devstack.exe",
    [string]$UserId = "$env:USERDOMAIN\$env:USERNAME"
)

$ErrorActionPreference = "Stop"
$taskName = "devstack-daemon"
if (-not (Test-Path $HomeDir)) {
    New-Item -ItemType Directory -Force -Path $HomeDir | Out-Null
}

$systemDir = Split-Path (Split-Path $Binary -Parent) -Parent
$systemDir = Join-Path $systemDir "system"
if (-not (Test-Path $systemDir)) {
    $systemDir = $HomeDir
}
$vbsPath = Join-Path $systemDir "devstack-daemon.vbs"
$vbsContent = "Set WshShell = CreateObject(""WScript.Shell"")`r`nWshShell.Run """"""$Binary"""" daemon run --home """"$HomeDir"""""", 0, False`r`n"
Set-Content -Path $vbsPath -Value $vbsContent -Encoding Ascii -Force

$action = New-ScheduledTaskAction -Execute "wscript.exe" -Argument "`"$vbsPath`"" -WorkingDirectory $HomeDir
$trigger = try {
    New-ScheduledTaskTrigger -AtLogOn -User $UserId
} catch {
    New-ScheduledTaskTrigger -AtLogOn
}
$principal = New-ScheduledTaskPrincipal -UserId $UserId -LogonType Interactive -RunLevel Limited
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit (New-TimeSpan -Days 0)
Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
Write-Output "registered $taskName for limited user $env:USERNAME"
