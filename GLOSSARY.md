# Silo ↔ riven

A Silo plugin that routes Silo's content requests to a riven-ts instance and reports their progress back to Silo. Silo is the host; riven-ts finds and fetches the media.

## Language

### Silo's side

**Request**:
A Silo user's ask for a movie or series, as Silo describes it: media type, title, and the external IDs Silo knows.
_Avoid_: order, job

**Connection**:
One configured riven-ts instance that requests can be routed to, reached by base URL and API key, with an optional TMDB key.
_Avoid_: instance, backend, server

**Quality tier**:
One quality Silo wants a request fulfilled at (e.g. 1080p, 2160p). riven-ts has no equivalent.
_Avoid_: profile

**Fulfillment target**:
Silo's record of one request on one connection at one quality tier, carrying a normalized status and an external ID.
_Avoid_: target request

### riven-ts's side

**Media item**:
riven-ts's record of a movie, show, season or episode in its library, with a state (indexed, scraped, … completed). It may not exist yet right after a request.
_Avoid_: library item

**Item request**:
riven-ts's record that something asked for media, with its own request state (requested, ongoing, …) and, for a show, the seasons asked for. Its state is separate from the media item's state.
_Avoid_: request (on its own, when riven-ts's record is meant)

**Item**:
What the plugin asks riven-ts for: a movie by its TMDB ID, or a show by its TVDB ID and riven-ts seasons.
_Avoid_: media (on its own)

### Identity

**External ID**:
An identifier for a movie or series in an outside catalogue: IMDB, TMDB or TVDB.

**Lookup ID**:
The external ID riven-ts tracks media by: TMDB for movies, TVDB for series. A movie known only by IMDB has none.
_Avoid_: primary ID

### Seasons

**Silo season**:
A season of a series as Silo numbers it, which follows TMDB. Season 0 is Specials.
_Avoid_: TMDB season, requested season (on its own)

**riven-ts season**:
A season of a show as riven-ts numbers it, which follows TVDB. The same episodes can sit in a riven-ts season with a different number from their Silo season.
_Avoid_: TVDB season

**Season matching**:
Finding the riven-ts seasons that hold a Silo season's episodes, by the Silo season's first air date and episode count.
_Avoid_: season resolution, renumbering
