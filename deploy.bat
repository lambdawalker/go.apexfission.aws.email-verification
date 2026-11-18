@echo off
setlocal
where go >nul 2>nul
if errorlevel 1 (
  echo Install Go 1.25+ and add it to PATH. 1>&2
  exit /b 1
)
pushd "%~dp0" || exit /b 1
set "GOWORK=off"
go run -modfile=tools/deploy/go.mod -mod=readonly github.com/lambdawalker/go.infra.composer/deploycli/cmd/infra-deploy %*
set "result=%errorlevel%"
popd
exit /b %result%
