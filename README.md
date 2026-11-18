# doctolib-mcp

A read-only, anonymous [MCP](https://modelcontextprotocol.io) server for [doctolib.fr](https://www.doctolib.fr).
An agent can search practitioners near a French postcode or town, read a practitioner's visit motives, places and
agendas, and see free slots and the earliest one. The server never logs in and never books: it gives the user the
practitioner's Doctolib page, where they book themselves.

It works with any MCP client over stdio: Claude Code, Claude Desktop, Hermes and others.

> **Not affiliated with Doctolib.** The server calls the JSON endpoints Doctolib's own website uses. They are not a
> public API and may change or stop working at any time. Using them may breach Doctolib's terms of use (CGU,
> article 19, which forbids scraping); you use this software at your own risk.

## Tools

| Tool | What it returns | Requests to Doctolib |
|---|---|---|
| `search_practitioners` | Practitioners and organisations near a postcode or town, with distance, area, languages, sector, online booking, a ready-made `availability_query` and the `profile_url` to book; Doctolib can filter by language, video consultation, regulation sector and a free slot within 1, 3, 7 or 14 days | 1 (plus 1–2 to geo.api.gouv.fr) |
| `practitioner_profile` | A practitioner's visit motives, which practice and agenda offer each one, new-patient restrictions, disabled agendas, places with phone numbers | 1 |
| `availability` | For 1 to 20 `availability_query` objects: status, earliest slot and free times over 15 days | 1 per query |

The server organises Doctolib's responses into stable fields and works out what can be worked out: the full name,
the distance in kilometres, whether a result is in the searched area, the slot status, the earliest slot. It never
drops a field: every field Doctolib returns either goes, with its value unchanged, into the organised structure,
or stays under its original name in the `other` object of the same level.

### Languages and labels

Agents pass languages as ISO 639 codes (`en`, `es`, `de`); the server turns them into Doctolib's own codes, which
are partly country codes (`gb` for English, `dz` for Kabyle) and partly language codes (`hu`, `ta`).
Its table holds the 71 codes Doctolib filters on, found by searching with every ISO 639-1 language code and ISO 3166
country code. The French labels of 33 of them come from Doctolib itself, its filter panel or practitioners' public
pages; the other 38 are inferred from codes whose meaning is plain. A code that is Doctolib's own spelling of another
language is refused (`gb`; `la`, Latin in ISO, is Lao on Doctolib), and an ISO code missing from the table is sent as
it is. Each language of a result carries Doctolib's code, the ISO code, the English name and the
French label; regulation sectors carry the label of Doctolib's filter panel.

### What the server infers

Some meanings come from other projects' notes or from observation and were not checked against Doctolib's
behaviour. The tool descriptions say so too.

- `allow_new_patients` is Doctolib's `allowNewPatients` flag on the matched visit motive, copied as it is; `false`
  is inferred to mean that motive takes no new patients.
- A visit motive's `new_patient_restrictions` lists agendas that take only existing patients for it (inferred). The
  server copies each entry onto the agendas it names and draws no conclusion: whether a practitioner can be booked
  is for the agent to judge, together with the slots.
- An agenda with `booking_temporary_disabled` cannot be booked for now (inferred); it is listed apart.
- Slot status, first rule that matches: `has_slots` (free slots in the window); `not_opened` (Doctolib says the
  slots are not opened yet); `next_slot_known` (Doctolib gives the next slot after the window); `none_visible` (no
  slot can be seen, which does not mean the practitioner is fully booked).
- `distance_km` is the straight-line (haversine) distance from Doctolib's search point; Doctolib orders results by
  distance and does not keep them inside the area. `in_area` is true when the result's postcode belongs to the
  searched place.

## Usage gate

- Every request to Doctolib passes a gate: at least 1 second apart, at most 600 an hour and 2000 a day. The caps
  are sized on heavy use of the website by hand and are there to stop a runaway loop.
- All server processes of the same user share the gate: the state lives in `$XDG_STATE_HOME/doctolib-mcp`
  (`~/.local/state/doctolib-mcp` by default) under a file lock, since every client on one machine goes out through
  the same IP address.
- When Doctolib blocks a request (HTTP 403 or 429 whatever the body, or an HTML page with a 2xx or 503 status), the server writes
  `blocked.json` in that directory and stops: every tool refuses to call Doctolib until a person deletes the file.
  The file holds the time, the endpoint, the status and the start of the block page, with its support code. Delete
  it only once you decide to try again.
- No retry, no cookie, no redirect followed. Requests carry the headers of an ordinary Chrome browser.
- Only the postcode or town name is sent to [geo.api.gouv.fr](https://geo.api.gouv.fr) to find the area; those
  requests do not count against the caps.

## Install

```sh
go install github.com/tio-plato/doctolib-mcp/cmd/doctolib-mcp@latest
```

or, with Nix:

```sh
nix run github:tio-plato/doctolib-mcp
```

It runs on Linux and macOS (the gate uses `flock`).

## Configuration

| Variable | Effect |
|---|---|
| `DOCTOLIB_MCP_CHROME_MAJOR` | Chrome major version in the User-Agent, such as `157`. The default is fixed at build time: Nix builds take nixpkgs' chromium version, `go install` builds a version that ages. An outdated browser version is itself unusual, so set this when your build is old. |
| `XDG_STATE_HOME` | Parent of the state directory. |

## Clients

The server is named `doctolib`, so the tools appear as `mcp__doctolib__search_practitioners` and so on.

Claude Code:

```sh
claude mcp add doctolib -- doctolib-mcp
```

Claude Desktop, in `claude_desktop_config.json` (give the full path if `doctolib-mcp` is not on the `PATH`
Claude Desktop sees):

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

Tests run against a local fake server with hand-written fixtures; they never reach Doctolib.
