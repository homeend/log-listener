@echo off
rem Makeless build helper for log-listener (Windows cmd).
rem Mirrors the Makefile targets so `make` is not required.
rem
rem Usage: build.cmd [target]
rem   build         local binary (default)
rem   build-static  CGO_ENABLED=0 static binary
rem   test          go test ./...
rem   vet           go vet ./...
rem   race          go test -race ./...
rem   cover         coverage summary
rem   clean         remove built binary
rem   help          show this list
setlocal
set "BINARY=log-listener.exe"
set "PKG=./..."
set "CMD=./cmd/log-listener"

rem Run from the script's own directory so it works from anywhere.
pushd "%~dp0"

set "TARGET=%~1"
if "%TARGET%"=="" set "TARGET=build"

if /i "%TARGET%"=="build"        goto build
if /i "%TARGET%"=="build-static" goto build_static
if /i "%TARGET%"=="test"         goto test
if /i "%TARGET%"=="vet"          goto vet
if /i "%TARGET%"=="race"         goto race
if /i "%TARGET%"=="cover"        goto cover
if /i "%TARGET%"=="clean"        goto clean
if /i "%TARGET%"=="help"         goto help
if /i "%TARGET%"=="-h"           goto help
if /i "%TARGET%"=="--help"       goto help

echo unknown target: %TARGET% (try build.cmd help) 1>&2
set "EXITCODE=2"
goto end

:build
go build -o "%BINARY%" "%CMD%" || goto fail
echo built .\%BINARY%
goto ok

:build_static
rem Windows has no fully-static-libc story; a CGO-free binary is the equivalent.
set "CGO_ENABLED=0"
go build -trimpath -ldflags "-s -w" -o "%BINARY%" "%CMD%" || goto fail
echo built static .\%BINARY%
goto ok

:test
go test "%PKG%" || goto fail
goto ok

:vet
go vet "%PKG%" || goto fail
goto ok

:race
go test -race "%PKG%" || goto fail
goto ok

:cover
go test -cover "%PKG%" || goto fail
goto ok

:clean
if exist "%BINARY%" del /q "%BINARY%"
echo removed .\%BINARY%
goto ok

:help
echo Usage: build.cmd [target]
echo   build         local binary (default)
echo   build-static  CGO_ENABLED=0 static binary
echo   test          go test ./...
echo   vet           go vet ./...
echo   race          go test -race ./...
echo   cover         coverage summary
echo   clean         remove built binary
echo   help          show this list
goto ok

:fail
set "EXITCODE=%ERRORLEVEL%"
goto end

:ok
set "EXITCODE=0"

:end
popd
endlocal & exit /b %EXITCODE%
