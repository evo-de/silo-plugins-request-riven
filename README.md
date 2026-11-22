# Silo Requests: Riven

> Forked from
> [olivertgwalton/silo-plugins-request-riven](https://github.com/olivertgwalton/silo-plugins-request-riven),
> which targets riven-rs. This fork targets riven-ts instead and has no webhook
> scan source.

A Silo plugin that sends Silo's movie and series requests to a
[riven-ts](https://github.com/rivenmedia/riven-ts) instance through its GraphQL
API, and reports their progress back to Silo. It implements
`request_router.v1`. Unlike the
[Seerr plugin](https://github.com/Silo-Community/silo-plugins-requests-seerr),
no Seerr instance is involved.

## Install

Silo's built-in catalogs do not list this plugin. Install it from this repo's
catalog, or upload a binary.

**From the catalog.** In Silo, add a plugin repository with this URL, then
install **Riven Requests** from it. Silo downloads the binary for its platform
from this repo's latest release.

```
https://raw.githubusercontent.com/evo-de/silo-plugins-request-riven/main/index.json
```

**From a binary.** Download the binary for Silo's platform from the
[latest release](https://github.com/evo-de/silo-plugins-request-riven/releases/latest),
or build it as described under [Build and test](#build-and-test). Upload it on
Silo's plugin admin page, or with a Silo admin API key.

```sh
curl -H "Authorization: Bearer $SILO_API_KEY" \
  -F archive=@plugin-linux-amd64 \
  "$SILO_URL/api/v2/admin/plugins/uploads"
```

Silo runs the binary to read its manifest. An upload replaces any installed
plugin with the same id, `silo.requests.riven`. That includes the upstream
riven-rs plugin.

## Connect Silo to riven-ts

Each Riven request server in Silo has a base URL and an API key. The plugin has
no other settings.

1. Set `RIVEN_SETTING__apiKey` on riven-ts to a key of at least 32 characters,
   for example the output of `openssl rand -hex 32`. If the setting is unset,
   riven-ts generates a new key on every start, and the plugin cannot
   authenticate.
2. If Silo reaches riven-ts from another host or container, set
   `RIVEN_SETTING__host=0.0.0.0`. By default riven-ts listens on localhost
   only.
3. In Silo, add a request server that uses the Riven plugin. Set the base URL
   to riven-ts's address. The default port is 3000, for example
   `http://riven:3000`. Set the API key to the value of
   `RIVEN_SETTING__apiKey`.
4. Route movies and series to the Riven server in Silo's request routing.
   Silo refuses Standard routing while a media type has more than one server
   of the same kind. Disable any other request server for those media types,
   or switch to Advanced routing.

The plugin sends the key as `Authorization: Bearer <key>`. **Test connection**
reads a field that riven-ts answers only for a valid key, so a wrong key fails
the test.

If Silo and riven-ts run in separate Docker Compose projects,
`http://localhost:3000` does not reach riven-ts from inside Silo's container.
Use `http://host.docker.internal:3000`, or put both containers on a shared
Docker network.

## How requests map

- The plugin submits each request with riven-ts's `requestItem` mutation.
  A series request asks for the seasons Silo names, or for every season but
  Specials when it names none. See [Seasons](#seasons).
- A movie needs a TMDB id and a series needs a TVDB id, because riven-ts tracks
  items by those ids. Silo fails a request that lacks the right id, even when
  it has an IMDb id. riven-ts accepts an IMDb-only request but fails it later.
- riven-ts picks the release itself and has no quality profiles. The plugin
  sends one request per connection and reports the same result for every
  quality Silo asks for.
- `requestedBy` is always `silo`. riven-ts has no per-user identity.
- riven-ts reports nothing back about duplicates or rejections. It answers
  `requestItem` before it processes the request, so a request that riven-ts
  drops stays `queued` in Silo.

## Seasons

A series request in Silo can name seasons. The plugin passes them to riven-ts
as `requestedSeasons`, and riven-ts fetches only those seasons. When riven-ts
already tracks some seasons of the show, it adds the new ones to its list.
Asking again for seasons riven-ts already tracks changes nothing. Once the list
covers every season but Specials, riven-ts turns it into a whole-show request
when it next indexes the show, so it also fetches seasons that air later.

There are two exceptions.

- If riven-ts already tracks the whole show, the plugin sends nothing, and the
  request shows "riven already tracks every season". riven-ts rebuilds its
  tracked seasons from the request's season list each time it indexes the
  show, so a list would drop every other season.
- riven-ts leaves Specials (season 0) out of a whole-show request. A request
  for the Specials of such a show fails, because adding them would drop the
  other seasons.

Silo numbers seasons like TMDB, and riven-ts numbers them like TVDB. The two
disagree for some series. Bleach, for example, has two seasons on TMDB and
seventeen on TVDB. So Silo also sends each requested season's first air date
and episode count. The plugin looks up the show's episodes through riven-ts's
TVDB indexer, takes that many episodes airing from that date, and asks riven-ts
for the seasons they belong to. Bleach's second TMDB season becomes TVDB season
17. riven-ts fetches whole seasons, so when a TMDB season covers part of a TVDB
season, riven-ts fetches all of that TVDB season. Specials, and a season
without an air date or episode count, keep their number.

A request for the whole series changes nothing when riven-ts tracks some
seasons of the show. riven-ts keeps its season list and cannot widen it to the
whole show.

Silo cannot request single episodes, and riven-ts's `requestItem` takes only
seasons, so the plugin requests whole seasons.

## How status maps

The plugin looks up the request with `itemRequestByExternalIds` and reads the
state of the request's primary media item. The request's own state is not
enough, because riven-ts marks a released movie's request `completed` as soon
as it indexes the movie.

| riven-ts state | Silo status |
| --- | --- |
| No request | `queued` |
| Request `failed` | `failed` |
| No media item yet | `queued` |
| Media `completed` | `completed` |
| Media `failed` or `paused` | `failed` |
| Media `scraped`, `downloaded`, or `partially_completed` | `downloading` |
| Media `indexed`, `unreleased`, or any other state | `queued` |

For a request that names seasons, the plugin reads the state of the riven-ts
seasons they map to instead of the whole show. Seasons that all share a state give that state.
Otherwise any finished season gives `partially_completed`, any season in
progress gives `scraped`, and the rest give `indexed`. A season riven-ts does
not track yet counts as not started.

Silo shows the media item's state as the external status. Before riven-ts
indexes the item, it shows the request's state.

## Library scans

This plugin does not scan downloads into Silo's library. riven-ts does that
through its own Silo plugin, `@repo/plugin-silo`, which you configure on the
riven-ts side.

## Build and test

```
go build ./... && go test ./...
```

To build a binary to upload, set the target platform. For example, for
linux/amd64:

```
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o dist/plugin-linux-amd64 .
```

`index.json` repeats `manifest.json` for the catalog. A test fails when the two
differ.

## Release

Run the **Release** workflow, or push a `v*` tag. The workflow builds
`plugin-linux-amd64`, `plugin-linux-arm64`, and `plugin-darwin-arm64`, and
publishes them with `checksums.txt` as a GitHub release. The catalog in
`index.json` always points at the latest release. A manual run releases
`manifest.json`'s version when the repo has no `v*` tag yet. After that, each
manual run bumps the patch version of the latest tag.

## Maintenance

This plugin follows the same structure as the community-maintained
[`silo-plugins-requests-seerr`](https://github.com/Silo-Community/silo-plugins-requests-seerr),
but is maintained independently in this repo. Security reports should follow
[`SECURITY.md`](SECURITY.md).

The plugin consumes the published
[`silo-plugin-sdk`](https://github.com/Silo-Server/silo-plugin-sdk); CI
rejects machine-local SDK replacement directives.

## License

Licensed under AGPL-3.0. See [`LICENSE`](LICENSE).
