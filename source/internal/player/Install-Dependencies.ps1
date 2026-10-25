param([switch]$Update, [string]$InstallDirectory = $PSScriptRoot)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$toolsDir = Join-Path $InstallDirectory 'tools'
New-Item -ItemType Directory -Force -Path $toolsDir | Out-Null
function Download-Atomic([string]$Uri, [string]$Destination) {
    $tempFile = "$Destination.part"
    try {
        Invoke-WebRequest -UseBasicParsing -Uri $Uri -OutFile $tempFile
        if ((Get-Item $tempFile).Length -lt 1000) { throw 'Downloaded file is unexpectedly small' }
        Move-Item -Force -LiteralPath $tempFile -Destination $Destination
    } finally { Remove-Item -Force -ErrorAction SilentlyContinue -LiteralPath $tempFile }
}
function Has-WebViewRuntime {
    foreach ($base in @(${env:ProgramFiles(x86)}, $env:ProgramFiles, $env:LOCALAPPDATA)) {
        if ($base -and (Test-Path (Join-Path $base 'Microsoft\EdgeWebView\Application\*\msedgewebview2.exe'))) { return $true }
    }
    return $false
}
$workDir = Join-Path ([IO.Path]::GetTempPath()) ('SayMusic-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory $workDir | Out-Null
try {
    if (-not (Has-WebViewRuntime)) {
        Write-Host 'Installing Microsoft Edge WebView2 Runtime...' -ForegroundColor Cyan
        $runtimeInstaller = Join-Path $workDir 'MicrosoftEdgeWebview2Setup.exe'
        Download-Atomic 'https://go.microsoft.com/fwlink/p/?LinkId=2124703' $runtimeInstaller
        $runtimeProcess = Start-Process -FilePath $runtimeInstaller -ArgumentList '/silent','/install' -Wait -PassThru
        if (($runtimeProcess.ExitCode -ne 0) -and ($runtimeProcess.ExitCode -ne 3010)) { throw "WebView2 installer failed: $($runtimeProcess.ExitCode)" }
        if (-not (Has-WebViewRuntime)) { throw 'WebView2 Runtime not found after installation. Restart Windows and try again.' }
    }
    if ($Update -or -not (Test-Path (Join-Path $toolsDir 'yt-dlp.exe'))) {
        Write-Host 'Installing yt-dlp...' -ForegroundColor Cyan
        Download-Atomic 'https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp.exe' (Join-Path $toolsDir 'yt-dlp.exe')
    }
    if ($Update -or -not (Test-Path (Join-Path $toolsDir 'deno.exe'))) {
        Write-Host 'Installing Deno...' -ForegroundColor Cyan
        $denoZip = Join-Path $workDir 'deno.zip'
        Download-Atomic 'https://github.com/denoland/deno/releases/latest/download/deno-x86_64-pc-windows-msvc.zip' $denoZip
        Expand-Archive -Force $denoZip (Join-Path $workDir 'deno')
        Copy-Item -Force (Join-Path $workDir 'deno/deno.exe') $toolsDir
    }
    if ($Update -or -not (Test-Path (Join-Path $toolsDir 'ffmpeg.exe'))) {
        Write-Host 'Installing FFmpeg...' -ForegroundColor Cyan
        $ffZip = Join-Path $workDir 'ffmpeg.zip'
        Download-Atomic 'https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip' $ffZip
        Expand-Archive -Force $ffZip (Join-Path $workDir 'ffmpeg')
        $ffBinary = Get-ChildItem (Join-Path $workDir 'ffmpeg') -Filter ffmpeg.exe -Recurse | Select-Object -First 1
        if (-not $ffBinary) { throw 'ffmpeg.exe not found in archive' }
        Copy-Item -Force $ffBinary.FullName $toolsDir
    }
    Write-Host 'Say Music components are ready.' -ForegroundColor Green
} finally { Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $workDir }
