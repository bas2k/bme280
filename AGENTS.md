# Repository Guidelines

## Project Structure & Module Organization

This repository provides the Go package `github.com/bas2k/bme280`, a Bosch BME280 sensor driver. Source files live at the repository root:

- `bme280.go`: bus interface, initialization, settings, measurements, and compensation.
- `strings.go`: generated string representations for sensor enums.
- `bme280_test.go`: unit tests and fake register buses.
- `README.md`: device setup, usage example, and measurement units.
- `format_check.sh` and `.github/workflows/ci.yml`: formatting and CI checks.

There is no application entry point or asset directory.

## Build, Test, and Development Commands

Use Go 1.26 or newer, as declared in `go.mod`.

- `go build ./...`: compile the library.
- `go test ./...`: run all tests without physical hardware.
- `go test -cover ./...`: run tests and report coverage.
- `go test -run TestSetSettings ./...`: run a focused test group.
- `go fmt ./...`: format Go source with standard tabs and spacing.
- `./format_check.sh`: check tracked Go files for formatting differences.
- `GOOS=linux GOARCH=arm go build ./...`: check the ARM32 build used in CI.

## Coding Style & Naming Conventions

Follow standard Go conventions: exported identifiers use PascalCase, internal identifiers use camelCase, and filenames use lowercase names. Preserve existing public API spellings such as `StandByTime`. Keep device communication behind the existing `ReadReg`/`WriteReg` interface and propagate communication errors. Do not hand-edit `strings.go`; regenerate it with `go generate ./...` when changing enums, with `stringer` installed.

## Testing Guidelines

Use the standard `testing` package, `TestXxx` names, table-driven cases, and descriptive subtests. Reuse fake buses for register behavior and injected errors; use `testing/synctest` for deterministic timing where appropriate. Add regression tests for calibration, compensation, mode transitions, and timeout changes. No numeric coverage threshold is configured. Document physical-sensor validation separately when performed.

## Commit & Pull Request Guidelines

History favors short, imperative subjects such as `Add Close() method`; optional prefixes identify areas, for example `CI:`. Keep commits focused. Pull requests should describe the behavior change, link relevant issues, and report checks performed. Explain any public API or measurement-unit changes, and update README examples when usage changes.

## Agent Instructions

When `.codegraph/` exists, use CodeGraph before searching or reading code to understand symbols and call paths. Preserve unrelated working-tree changes.
