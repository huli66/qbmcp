$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Console]::InputEncoding = [Text.UTF8Encoding]::new($false)
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
try {
    $request = [Console]::In.ReadToEnd() | ConvertFrom-Json
    $scheduler = New-Object -ComObject 'Schedule.Service'
    $scheduler.Connect()
    $folder = $scheduler.GetFolder('\')
    function Find-Task([string]$name) {
        try { return $folder.GetTask($name) }
        catch {
            $cause = $_.Exception
            while ($cause.InnerException) { $cause = $cause.InnerException }
            if ($cause.HResult -ne -2147024894) { throw }
            return $null
        }
    }
    $task = Find-Task $request.name
    $logonName = $request.name + '-logon'
    $logonTask = Find-Task $logonName
    $watchName = $request.name + '-watch'
    $watchTask = Find-Task $watchName
    function New-Definition([string]$runner, [bool]$recovery) {
        $definition = $scheduler.NewTask(0)
        $definition.RegistrationInfo.Description = 'qbmcp current-user background MCP bridge'
        $definition.Principal.UserId = $request.sid
        $definition.Principal.LogonType = 3 # TASK_LOGON_INTERACTIVE_TOKEN
        $definition.Principal.RunLevel = 0 # TASK_RUNLEVEL_LUA
        $settings = $definition.Settings
        $settings.Enabled = $true
        $settings.Hidden = $true
        $settings.AllowDemandStart = $true
        $settings.DisallowStartIfOnBatteries = $false
        $settings.StopIfGoingOnBatteries = $false
        $settings.ExecutionTimeLimit = 'PT0S'
        $settings.MultipleInstances = 2 # IgnoreNew
        if ($recovery) {
            $settings.RestartInterval = 'PT1M'
            $settings.RestartCount = 999
        }
        $settings.StartWhenAvailable = $false
        $action = $definition.Actions.Create(0)
        $action.Path = $request.powershell
        $action.Arguments = '-NoProfile -NonInteractive -WindowStyle Hidden -EncodedCommand ' + $runner
        $action.WorkingDirectory = $request.home
        return $definition
    }
    if ($request.action -eq 'ensure') {
        # Re-registering a running task can discard that run's recovery state.
        # Keep the supervisor definition stable; login startup uses a separate,
        # short-lived task that starts the existing supervisor task.
        if (-not $task) {
            $definition = New-Definition $request.runner $true
            $task = $folder.RegisterTaskDefinition($request.name, $definition, 6, $request.sid, $null, 3, $null)
        }
        if (-not $watchTask) {
            $definition = New-Definition $request.watchRunner $false
            $definition.Settings.ExecutionTimeLimit = 'PT1M'
            $trigger = $definition.Triggers.Create(1) # TASK_TRIGGER_TIME
            $trigger.StartBoundary = (Get-Date).AddMinutes(1).ToString('yyyy-MM-ddTHH:mm:ss')
            $trigger.Repetition.Interval = 'PT1M'
            $trigger.Enabled = $true
            $watchTask = $folder.RegisterTaskDefinition($watchName, $definition, 6, $request.sid, $null, 3, $null)
        }
        if ($null -ne $request.enabled) {
            if ($request.enabled -and -not $logonTask) {
                $definition = New-Definition $request.logonRunner $false
                $definition.Settings.ExecutionTimeLimit = 'PT1M'
                $trigger = $definition.Triggers.Create(9)
                $trigger.UserId = $request.sid
                $trigger.Enabled = $true
                $logonTask = $folder.RegisterTaskDefinition($logonName, $definition, 6, $request.sid, $null, 3, $null)
            } elseif (-not $request.enabled -and $logonTask) {
                $folder.DeleteTask($logonName, 0)
                $logonTask = $null
            }
        }
    } elseif ($request.action -eq 'run') {
        if (-not $task) { throw 'qbmcp task is not installed' }
        $null = $task.Run($null)
    } elseif ($request.action -eq 'stop') {
        if ($task) { $task.Stop(0) }
    } elseif ($request.action -eq 'delete') {
        if ($watchTask) { $watchTask.Stop(0); $folder.DeleteTask($watchName, 0); $watchTask = $null }
        if ($logonTask) { $folder.DeleteTask($logonName, 0); $logonTask = $null }
        if ($task) { $folder.DeleteTask($request.name, 0); $task = $null }
    } elseif ($request.action -ne 'query') {
        throw 'Unknown scheduler action'
    }
    $result = @{ installed = $false; name = $request.name; state = 0; autostart = [bool]$logonTask; watch_installed = [bool]$watchTask; last_result = 0 }
    if ($task) {
        $result.installed = $true
        $result.state = [int]$task.State
        $result.last_result = [long]$task.LastTaskResult
    }
    [Console]::Out.WriteLine(($result | ConvertTo-Json -Compress))
} catch {
    [Console]::Error.WriteLine($_.Exception.Message)
    exit 1
}
