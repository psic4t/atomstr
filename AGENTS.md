# AGENTS.md

## Build/Lint/Test Commands
- Build: `make atomstr` or `go build -o atomstr`
- Lint: `go vet ./...` and `go fmt ./...`
- Test: No tests present; use `go test` if added
- Run single test: `go test -run TestName` (if tests exist)

## Code Style Guidelines
- Imports: Group standard library, then third-party; use blank imports for side effects
- Formatting: Run `go fmt` before commits; use tabs for indentation
- Types: Export structs/functions with capital letters; use PascalCase for types
- Naming: camelCase for variables/functions; UPPER_CASE for constants
- Error handling: Log errors with `log.Println` and continue; ignore with `_` if appropriate
- Comments: Minimal; use for complex logic only
- Conventions: Follow effective Go patterns; keep functions short

## Git Workflow
- Conditional commits: Always run `go vet ./...`, `go build -o atomstr`, and `go test ./...` before committing. Only commit if all three pass.
- Commit style: Short lowercase messages describing the change (see git log for examples)