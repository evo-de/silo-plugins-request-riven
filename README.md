# Silo Requests: Riven

A Silo plugin that sends Silo's movie and series requests to a
[riven-ts](https://github.com/rivenmedia/riven-ts) instance through its GraphQL
API, and reports their progress back to Silo. It implements
`request_router.v1`. Unlike the
[Seerr plugin](https://github.com/Silo-Community/silo-plugins-requests-seerr),
no Seerr instance is involved.

## Connect Silo to riven-ts

Each Riven connection in Silo has a base URL and an API key. The plugin has no
other settings.

1. Set `RIVEN_SETTING__apiKey` on riven-ts to a key of at least 32 characters,
   for example the output of `openssl rand -hex 32`. If the setting is unset,
   riven-ts generates a new key on every start, and the plugin cannot
   authenticate.
2. If Silo reaches riven-ts from another host or container, set
   `RIVEN_SETTING__host=0.0.0.0`. By default riven-ts listens on localhost
   only.
3. In Silo, add a Riven connection. Set the base URL to riven-ts's address. The
   default port is 3000, for example `http://riven:3000`. Set the API key to the
   value of `RIVEN_SETTING__apiKey`.

The plugin sends the key as `Authorization: Bearer <key>`. **Test connection**
reads a field that riven-ts answers only for a valid key, so a wrong key fails
the test.

If Silo and riven-ts run in separate Docker Compose projects,
`http://localhost:3000` does not reach riven-ts from inside Silo's container.
Use `http://host.docker.internal:3000`, or put both containers on a shared
Docker network.

## How requests map

- The plugin submits each request with riven-ts's `requestItem` mutation.
  A series request asks for every non-special season.
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

Silo shows the media item's state as the external status. Before riven-ts
indexes the item, it shows the request's state.

## Library scans

This plugin does not scan downloads into Silo's library. riven-ts does that
through its own Silo plugin, `@repo/plugin-silo`, which you configure on the
riven-ts side.

## Build / test

```
go build ./... && go test ./...
```

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
