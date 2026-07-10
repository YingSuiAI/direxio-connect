param(
    [Parameter()]
    [string]$RepositoryRoot = "."
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$resolvedRoot = (Resolve-Path -LiteralPath $RepositoryRoot).Path
$git = Get-Command git -ErrorAction Stop

$startInfo = [System.Diagnostics.ProcessStartInfo]::new()
$startInfo.FileName = $git.Source
$startInfo.UseShellExecute = $false
$startInfo.RedirectStandardOutput = $true
$startInfo.RedirectStandardError = $true
$startInfo.ArgumentList.Add("-C")
$startInfo.ArgumentList.Add($resolvedRoot)
$startInfo.ArgumentList.Add("ls-files")
$startInfo.ArgumentList.Add("--eol")
$startInfo.ArgumentList.Add("-z")

$process = [System.Diagnostics.Process]::new()
$process.StartInfo = $startInfo
if (-not $process.Start()) {
    throw "Failed to start git ls-files."
}
$stdout = $process.StandardOutput.ReadToEnd()
$stderr = $process.StandardError.ReadToEnd()
$process.WaitForExit()
if ($process.ExitCode -ne 0) {
    throw "git ls-files --eol failed with exit code $($process.ExitCode): $stderr"
}

$indexViolations = [System.Collections.Generic.List[string]]::new()
$worktreeViolations = [System.Collections.Generic.List[string]]::new()
foreach ($record in $stdout.Split([char]0, [System.StringSplitOptions]::RemoveEmptyEntries)) {
    $tab = $record.IndexOf([char]9)
    if ($tab -lt 0) {
        throw "Unexpected git ls-files --eol record: $record"
    }

    $metadata = $record.Substring(0, $tab)
    $path = $record.Substring($tab + 1)
    $match = [regex]::Match($metadata, '^i/(?<index>\S+)\s+w/(?<worktree>\S+)\s+attr/')
    if (-not $match.Success) {
        throw "Unexpected git ls-files --eol metadata for '$path': $metadata"
    }

    $indexEol = $match.Groups['index'].Value
    if ($indexEol -eq '-text') {
        continue
    }
    if ($indexEol -eq 'crlf' -or $indexEol -eq 'mixed') {
        $indexViolations.Add($path)
    }

    $fullPath = Join-Path $resolvedRoot $path
    if (Test-Path -LiteralPath $fullPath -PathType Leaf) {
        $item = Get-Item -LiteralPath $fullPath -Force
        if (($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -eq 0) {
            $bytes = [System.IO.File]::ReadAllBytes($fullPath)
            if ($bytes -contains [byte]13) {
                $worktreeViolations.Add($path)
            }
        }
    }
}

if ($indexViolations.Count -gt 0) {
    [Console]::Error.WriteLine("Tracked text files must use LF in the Git index:")
    foreach ($path in $indexViolations) {
        [Console]::Error.WriteLine("  $path")
    }
}
if ($worktreeViolations.Count -gt 0) {
    [Console]::Error.WriteLine("Tracked text files must not contain CR bytes in the working tree:")
    foreach ($path in $worktreeViolations) {
        [Console]::Error.WriteLine("  $path")
    }
}
if ($indexViolations.Count -gt 0 -or $worktreeViolations.Count -gt 0) {
    exit 1
}

[Console]::WriteLine("Tracked text LF check passed ($($stdout.Split([char]0, [System.StringSplitOptions]::RemoveEmptyEntries).Count) files inspected).")
