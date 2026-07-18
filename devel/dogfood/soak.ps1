param(
    [Parameter(Mandatory = $true)]
    [string]$StateDir,
    [Parameter(Mandatory = $true)]
    [string]$EvidenceDir,
    [Parameter(Mandatory = $true)]
    [ValidateRange(1, 2147483647)]
    [int]$DurationSeconds,
    [string]$JobmanBin = 'jobman.exe'
)

$ErrorActionPreference = 'Stop'
New-Item -ItemType Directory -Force $StateDir, $EvidenceDir | Out-Null
$deadline = [DateTime]::UtcNow.AddSeconds($DurationSeconds)
$iteration = 0
$summaryPath = Join-Path $EvidenceDir 'soak-summary.csv'

function Invoke-Jobman {
    param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)
    $output = & $JobmanBin --state-dir $StateDir @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "jobman $($Arguments -join ' ') exited $LASTEXITCODE"
    }
    return $output
}

while ([DateTime]::UtcNow -lt $deadline) {
    $iteration++
    $successId = Invoke-Jobman run --name "soak-success-$iteration" `
        --log-segment-bytes 64 --log-segments 4 -- powershell.exe -NoProfile `
        -Command '1..40 | ForEach-Object { [Console]::Out.WriteLine("stdout-{0:D3}" -f $_); [Console]::Error.WriteLine("stderr-{0:D3}" -f $_) }'
    $timeoutId = Invoke-Jobman run --name "soak-timeout-$iteration" `
        --run-timeout 100ms -- powershell.exe -NoProfile -Command 'Start-Sleep 2'
    $retryId = Invoke-Jobman run --name "soak-retry-$iteration" --retries 2 `
        --retryable-exit-code 17 --retry-delay 10ms -- powershell.exe `
        -NoProfile -Command 'exit 17'
    $inputId = Invoke-Jobman run --name "soak-input-$iteration" --stdin live -- `
        powershell.exe -NoProfile -Command '$input | Out-Null'

    "iteration $iteration" | & $JobmanBin --state-dir $StateDir input $inputId | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "input delivery failed in iteration $iteration" }
    $null | & $JobmanBin --state-dir $StateDir input --eof $inputId | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "input EOF failed in iteration $iteration" }

    foreach ($jobId in @($successId, $timeoutId, $retryId, $inputId)) {
        Invoke-Jobman wait $jobId | Out-File -Append (Join-Path $EvidenceDir 'waits.log')
    }

    Invoke-Jobman doctor --json | Out-File -Encoding utf8 (Join-Path $EvidenceDir 'doctor-latest.json')
    Invoke-Jobman clean --older-than 0s | Out-File -Encoding utf8 (Join-Path $EvidenceDir 'clean-latest.txt')
    $stateBytes = (Get-ChildItem -LiteralPath $StateDir -File -Recurse |
        Measure-Object -Property Length -Sum).Sum
    $processes = @(Get-Process -Name jobman -ErrorAction SilentlyContinue)
    $sample = [pscustomobject]@{
        Iteration = $iteration
        TimestampUtc = [DateTime]::UtcNow.ToString('o')
        StateBytes = [long]$stateBytes
        JobmanProcesses = $processes.Count
        Handles = [long](($processes | Measure-Object -Property HandleCount -Sum).Sum)
        WorkingSetBytes = [long](($processes | Measure-Object -Property WorkingSet64 -Sum).Sum)
    }
    if (Test-Path -LiteralPath $summaryPath) {
        $sample | Export-Csv -NoTypeInformation -Append $summaryPath
    } else {
        $sample | Export-Csv -NoTypeInformation $summaryPath
    }
}

Invoke-Jobman doctor --json | Out-File -Encoding utf8 (Join-Path $EvidenceDir 'doctor-final.json')
Write-Host "Soak completed: $iteration iterations; evidence: $EvidenceDir"
