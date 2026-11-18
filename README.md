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

```sh
go install github.com/tio-plato/doctolib-mcp/cmd/doctolib-mcp@latest
```

or with Nix:

```sh
nix run github:tio-plato/doctolib-mcp
```

Linux and macOS only: the rate limiter uses `flock`.

## Configuration

`DOCTOLIB_MCP_CHROME_MAJOR` sets the Chrome major version in the User-Agent, e.g. `157`. Nix builds use nixpkgs'
chromium version; `go install` uses a built-in default that goes stale, so set it if your build is old.

## Clients

Claude Code:

```sh
claude mcp add doctolib -- doctolib-mcp
```

Claude Desktop, in `claude_desktop_config.json` (use the full path if `doctolib-mcp` isn't on Claude Desktop's
`PATH`):

```json
{
  "mcpServers": {
    "doctolib": {"command": "doctolib-mcp"}
  }
}
```

Hermes, in `config.yaml`:

```yaml
mcp_servers:
  doctolib:
    command: doctolib-mcp
```

## Development

```sh
nix develop       # Go, gopls, golangci-lint
nix flake check   # build, tests, golangci-lint
```

## License

[MIT](LICENSE).
