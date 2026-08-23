# Generates _data/passport-mock.crt and .key for local TLS (RSA for XP).
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $root

New-Item -ItemType Directory -Force -Path "_data" | Out-Null
go run .\scripts\gencert -out _data

Write-Host ""
Write-Host "Local XP lab (staging hostnames via hosts file):"
Write-Host "  1. XP hosts:"
Write-Host "       192.168.1.11 passport-staging.lunastore.app memberservices-staging.lunastore.app"
Write-Host "  2. Import passport-local.reg; trust _data\passport-mock.crt"
Write-Host "  3. go run .\cmd\lunapassport -http :8080 ``"
Write-Host "       -passport-host passport-staging.lunastore.app ``"
Write-Host "       -memberservices-host memberservices-staging.lunastore.app ``"
Write-Host "       -passport-cookie-domain .lunastore.app"
Write-Host "  4. Elevated: go run .\cmd\tlspproxy"
Write-Host ""
Write-Host "No public DNS needed when hosts points those names at your lab IP."
