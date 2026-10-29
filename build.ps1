# 交叉编译：Windows / Linux 全平台产物
$ErrorActionPreference = 'Stop'
$env:CGO_ENABLED = '0'

$targets = @(
    @{ Out = 'build\gateway-manager-windows-amd64.exe';               GOOS = 'windows'; GOARCH = 'amd64' },
    @{ Out = 'build\gateway-manager-windows-arm64.exe'; GOOS = 'windows'; GOARCH = 'arm64' },
    @{ Out = 'build\gateway-manager-linux-amd64';       GOOS = 'linux';   GOARCH = 'amd64' },
    @{ Out = 'build\gateway-manager-linux-arm64';       GOOS = 'linux';   GOARCH = 'arm64' },
    @{ Out = 'build\gateway-manager-linux-armv7';       GOOS = 'linux';   GOARCH = 'arm';   },
    @{ Out = 'build\gateway-manager-darwin-amd64';      GOOS = 'darwin';  GOARCH = 'amd64' },
    @{ Out = 'build\gateway-manager-darwin-arm64';      GOOS = 'darwin';  GOARCH = 'arm64' }
)

New-Item -ItemType Directory -Force -Path build | Out-Null
foreach ($t in $targets) {
    $env:GOOS = $t.GOOS
    $env:GOARCH = $t.GOARCH
    Write-Host ("building {0}/{1} -> {2}" -f $t.GOOS, $t.GOARCH, $t.Out)
    go build -trimpath -ldflags "-s -w -X main.version=2.1.2" -o $t.Out .
    if ($LASTEXITCODE -ne 0) { throw "build failed: $($t.Out)" }
}
Write-Host 'done.'
