[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [ValidateNotNullOrEmpty()]
    [string]$SentinelHome,

    [Parameter(Mandatory)]
    [ValidateNotNullOrEmpty()]
    [string]$TempRoot
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Get-CanonicalPath {
    param([Parameter(Mandatory)][string]$Path)

    $fullPath = [System.IO.Path]::GetFullPath($Path)
    $pathRoot = [System.IO.Path]::GetPathRoot($fullPath)
    if ($fullPath.Equals($pathRoot, [System.StringComparison]::OrdinalIgnoreCase)) {
        return $fullPath
    }
    return $fullPath.TrimEnd(
        [System.IO.Path]::DirectorySeparatorChar,
        [System.IO.Path]::AltDirectorySeparatorChar
    )
}

$comparison = if ($IsWindows) {
    [System.StringComparison]::OrdinalIgnoreCase
} else {
    [System.StringComparison]::Ordinal
}
$directorySeparator = [System.IO.Path]::DirectorySeparatorChar
$altDirectorySeparator = [System.IO.Path]::AltDirectorySeparatorChar

$canonicalSentinelHome = Get-CanonicalPath -Path $SentinelHome
$canonicalTempRoot = Get-CanonicalPath -Path $TempRoot
$allowedTempRoots = [System.Collections.Generic.List[string]]::new()
$allowedTempRoots.Add((Get-CanonicalPath -Path ([System.IO.Path]::GetTempPath())))
if (-not [string]::IsNullOrWhiteSpace($env:RUNNER_TEMP)) {
    $allowedTempRoots.Add((Get-CanonicalPath -Path $env:RUNNER_TEMP))
}

$tempRootAllowed = $false
foreach ($allowedRoot in $allowedTempRoots) {
    if ($canonicalTempRoot.Equals($allowedRoot, $comparison)) {
        $tempRootAllowed = $true
        break
    }
}
if (-not $tempRootAllowed) {
    throw "TempRoot is not the canonical RUNNER_TEMP or system TempDir: $canonicalTempRoot"
}

$safePrefix = $canonicalTempRoot.TrimEnd($directorySeparator, $altDirectorySeparator) + $directorySeparator
if ($canonicalSentinelHome.Equals($canonicalTempRoot, $comparison) -or
    -not $canonicalSentinelHome.StartsWith($safePrefix, $comparison)) {
    throw "SentinelHome must be a strict child of TempRoot: $canonicalSentinelHome"
}

$forbiddenHomes = [System.Collections.Generic.List[string]]::new()
$profileFolder = [Environment]::GetFolderPath([Environment+SpecialFolder]::UserProfile)
if (-not [string]::IsNullOrWhiteSpace($profileFolder)) {
    $forbiddenHomes.Add((Get-CanonicalPath -Path $profileFolder))
}
foreach ($candidate in @($env:HOME, $env:USERPROFILE, $HOME)) {
    if (-not [string]::IsNullOrWhiteSpace($candidate)) {
        $forbiddenHomes.Add((Get-CanonicalPath -Path $candidate))
    }
}
foreach ($forbiddenHome in $forbiddenHomes) {
    if ($canonicalSentinelHome.Equals($forbiddenHome, $comparison)) {
        throw "SentinelHome must not equal a real/current profile or HOME: $canonicalSentinelHome"
    }
}

if (Test-Path -LiteralPath $canonicalSentinelHome) {
    $item = Get-Item -LiteralPath $canonicalSentinelHome -Force
    if (($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "SentinelHome must not be a reparse point or symlink: $canonicalSentinelHome"
    }
    $resolvedSentinel = (Resolve-Path -LiteralPath $canonicalSentinelHome).Path
    if (-not $resolvedSentinel.StartsWith($safePrefix, $comparison)) {
        throw "Resolved SentinelHome escapes TempRoot: $resolvedSentinel"
    }
}

Write-Output $canonicalSentinelHome
