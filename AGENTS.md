# Agent instructions

doctolib-mcp is a read-only, anonymous MCP server for doctolib.fr, written in Go. See README.md for what it
does.

## Agent git policy

- Target branch: `main`.
- Setup: `nix develop -c true`.
- Checks: `nix flake check`. It builds the package, which runs every test, and runs golangci-lint.
- Landing: push the commit SHA to `main` (`git push origin <sha>:main`).
- Shipping: cut a release when the user asks, from the card that asked. Tag a commit already on `origin/main` whose
  `ci` run there passed: `git tag -s vX.Y.Z -m vX.Y.Z <sha>`, then `git push origin vX.Y.Z`. CI checks it, builds a
  draft release, smoke-tests it on every platform and publishes it. Don't create the release on GitHub yourself: CI
  would upload into it while it is already public, before the smoke test has run.
- Never move or delete a pushed tag: the Go module proxy and checksum database keep the first content they saw,
  so `go install` fails for anyone who got the old one. If a release is broken, fix it and release the next
  version; delete a draft that failed its smoke test.
- Versions follow SemVer: `feat` bumps the minor version, only `fix` the patch. Before 1.0, breaking changes bump
  the minor version too.
- Commit messages follow Conventional Commits.

## No personal information

- The repository holds no personal information, and no real health query: no speciality, language, address,
  postcode or practitioner that someone actually searched for. This covers code, tests, fixtures, the README,
  tool descriptions and commit messages, for the whole history.
- Fixtures, examples and descriptions use made-up data: neutral specialities such as `dentiste`, neutral postcodes
  such as `75001`, invented names such as `Ada EXAMPLE`, and languages such as `gb` or `es`.
- Never copy a real Doctolib response into `testdata/`: write fixtures by hand in the same shape.

## Doctolib

- Every Doctolib request goes through `internal/gate`: no request bypasses the block marker, the caps or the
  interval. No retry, no cookie, no redirect followed.
- Tests talk only to the loopback fake server. A live request to Doctolib from development needs the user's
  agreement, since a block hits the user's own IP address.

## Error handling

From [go-react-practice's go-error-handling.md](https://github.com/oott123/skills/blob/cf758dcbb59fa322c24c16b74143479f5b101337/go-react-practice/go-error-handling.md):

1. Wrap an error from another package or an interface method with `fmt.Errorf("<what this layer was doing>: %w",
   err)`. The text is a lowercase verb phrase (`read request log: %w`), without `failed to`, `error` or
   `unable to`, without trailing punctuation, and does not repeat what the cause already says.
2. Functions in the same package may pass errors through unchanged.
3. An error a function creates itself includes its key arguments.
4. Use `%w` for each error one `fmt.Errorf` references; collect parallel errors with `errors.Join`.
5. Errors are printed once, at the outermost layer: `main` writes them to stderr, and the tool handlers turn them
   into MCP tool errors. No layer both logs and returns the same error.
6. stdout is the MCP channel: anything else goes to stderr.
