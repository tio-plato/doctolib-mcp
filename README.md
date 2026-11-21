# doctolib-mcp

An MCP server for [doctolib.fr](https://www.doctolib.fr): search practitioners near a French postcode or town, read
their visit motives and check free slots. It is read-only and anonymous: it never logs in or books, and gives you
the practitioner's Doctolib page to book on.

It talks stdio, so any MCP client works: Claude Code, Claude Desktop, Hermes and others.

> **Not affiliated with Doctolib.** It uses the JSON endpoints behind Doctolib's website, which are not a public
> API and can change or break at any time. Scraping is against Doctolib's terms of use (CGU, article 19); use it
> at your own risk.

## Tools

| Tool | Returns |
|---|---|
| `search_practitioners` | Practitioners near a postcode or town, with distance, languages, sector and profile link. Filters: language, video consultation, sector, free slot within 1–14 days. |
| `practitioner_profile` | A practitioner's visit motives, practices, agendas and phone numbers. |
| `availability` | Earliest slot and free times over 15 days, for up to 20 practitioners (one request each). |

Doctolib fields the server doesn't map are kept as they are under `other`. A few meanings, such as
`allow_new_patients`, are inferred rather than documented; the tool descriptions say which.

## Rate limits

- Requests to Doctolib are at least 1 second apart, at most 600 an hour and 2000 a day. Every server process of the
  user shares these limits through `$XDG_STATE_HOME/doctolib-mcp` (default `~/.local/state/doctolib-mcp`).
- If Doctolib blocks a request (403, 429 or an HTML challenge page), the server writes `blocked.json` there, with
  the block page's support code, and makes no more requests until you delete it.
- Places are looked up on [geo.api.gouv.fr](https://geo.api.gouv.fr); only the postcode or town name is sent, and
  these lookups don't count against the limits.

## Install

Give your agent this repository's URL: it can follow the steps below on Linux or macOS, x86_64 or ARM, without Go
or Nix. Linux and macOS only: the rate limiter uses `flock`. On Windows, install it in WSL.

Each release has one archive per system, `doctolib-mcp_<os>_<arch>.tar.gz`, where `<os>` is `linux` or `darwin` and
`<arch>` is `amd64` for x86_64 or `arm64` for aarch64 and arm64. The commands below pick the archive, download it and
`checksums.txt` from the latest release, stop if the checksum doesn't match, and print the program's absolute path.

Into `~/.local/bin`:

```sh
sh -eu <<'EOF'
case $(uname -s) in Linux) os=linux ;; Darwin) os=darwin ;; *) echo "unsupported: $(uname -s)" >&2; exit 1 ;; esac
case $(uname -m) in x86_64) arch=amd64 ;; aarch64 | arm64) arch=arm64 ;; *) echo "unsupported: $(uname -m)" >&2; exit 1 ;; esac
file=doctolib-mcp_${os}_${arch}.tar.gz
url=https://github.com/tio-plato/doctolib-mcp/releases/latest/download
tmp=$(mktemp -d)
cd "$tmp"
curl -fsSLO "$url/$file"
curl -fsSLO "$url/checksums.txt"
line=$(grep "  $file\$" checksums.txt) || { echo "$file is not in checksums.txt" >&2; exit 1; }
if command -v sha256sum >/dev/null; then
  echo "$line" | sha256sum -c -
else
  echo "$line" | shasum -a 256 -c -
fi
mkdir -p "$HOME/.local/bin"
tar -xzf "$file" -C "$HOME/.local/bin" doctolib-mcp
rm -rf "$tmp"
echo "$HOME/.local/bin/doctolib-mcp"
EOF
```

To update, run the same commands again and restart the client.

With Go or Nix, these work too:

```sh
go install github.com/tio-plato/doctolib-mcp/cmd/doctolib-mcp@latest   # latest tag
nix run github:tio-plato/doctolib-mcp                                    # follows main
```

## Configuration

`DOCTOLIB_MCP_CHROME_MAJOR` sets the Chrome major version in the User-Agent, e.g. `157`. Release archives and Nix
builds use the chromium version of the nixpkgs they were built with; `go install` uses a built-in default. Either
goes stale: if your install is old, set the variable or install again. Hermes and OpenClaw pass only a few variables
to an MCP server, so set it under the server's `env` there.

## Clients

Use the absolute path the install printed: the client doesn't always have `~/.local/bin` in its `PATH`.

Claude Code:

```sh
claude mcp add doctolib -- /path/to/doctolib-mcp
```

Claude Desktop, in `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "doctolib": {"command": "/path/to/doctolib-mcp"}
  }
}
```

Hermes, in `~/.hermes/config.yaml`:

```yaml
mcp_servers:
  doctolib:
    command: /path/to/doctolib-mcp
```

OpenClaw:

```sh
openclaw mcp add doctolib --command /path/to/doctolib-mcp
```

Other clients take the same `mcpServers` JSON. The rate-limit state needs no `env` unless you set `XDG_STATE_HOME`:
Hermes and OpenClaw both pass `HOME`, so it stays in `~/.local/state/doctolib-mcp`. If you do set it, add it to the
`env` of clients that don't pass it on, such as OpenClaw, so that every client shares one state directory and its
limits.

## Development

```sh
nix develop       # Go, gopls, golangci-lint, GoReleaser
nix flake check   # build, tests, golangci-lint
```

## License

[MIT](LICENSE).
