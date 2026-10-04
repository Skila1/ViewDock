# Changelog

## 0.4.5

- party members share one Jellyfin stream, and downloads rest three minutes ahead

## 0.4.4

- offer an update only once its image is published

## 0.4.3

- keep downloading when the buffer cache is full, resume from the server after a reopen

## 0.4.2

- hold five seconds ahead in the player for buffered streams

## 0.4.1

- download one segment at a time until Jellyfin answers

## 0.4.0

- keep Jellyfin remuxes alive while paused and reject cut-short segments

## 0.3.9

- Resuming a 4K Jellyfin film could load forever: Jellyfin sometimes answers the first segment after a jump with an empty response, and the player then switched to Jellyfin's backup H.264 re-encode, which is slower than real time. Empty segments are now retried, and the backup re-encode is never used while the original video plays (a device that cannot decode it still falls back to H.264 properly).
- The player keeps about two segments in the browser's video buffer during cached 4K playback, so it no longer hits Chrome's buffer limit.

## 0.3.8

- Closing the player could erase the resume point: it sent position 0 because the video had already been removed from the page. It now sends the last position it played, and the server ignores a stop at 0 when it knows where the viewer was.
- Watch history lists each title once with its latest date and current progress, instead of an entry every few seconds after a title was finished. Removing an entry removes that title's history.
- Admin, Groups: groups are listed by rank (Superadmin first), and Create group opens a dialog with name, description, permissions and templates (Viewer, Uploader, Library manager, Moderator).
- Admin, Households: largest first, and Create household opens a dialog with templates (Family, Couple, Shared house, Young kids) that also create the first invites and show their codes.
- Admin, Users: one table of everyone, searchable and filterable (new, administrators, disabled), with accounts marked New for a week after registering, or after connecting Discord when Discord sign-in replaces registration. Select several to enable, disable, change group, set a content restriction or delete them together. Click a person for everything about the account: name, group (changeable), content restriction, household, Discord, personal library access with downloads, password reset, and disable or delete.

## 0.3.7

- 4K Jellyfin films could freeze mid-playback with the next part already downloaded: the player kept 30 seconds behind the playhead in the browser's video buffer, which at 4K remux bitrates overflows Chrome's limit, so a segment failed to load and playback stopped at the gap. With the buffer cache the player now keeps only a few seconds in that buffer (the rest plays from the cache at once) and steps over small gaps.
- A download from the media server that stalls is retried after 15 seconds without data instead of holding a download slot until it finishes.
- The seek bar shows what is stored on this device as a lighter shade inside the bar, under the progress, instead of a separate line.

## 0.3.6

- A Jellyfin title left paused in a background tab could end its stream (browsers slow hidden tabs' timers to once a minute, and the server ended sessions after 45 seconds), and the player then kept loading forever. External-source sessions now last 5 minutes without a check-in, the player checks in as soon as the tab is visible again, and a session that ended anyway reopens at the same position, still paused if it was.
- Resuming a Jellyfin title no longer downloads the first minutes of the film before jumping to the resume point; it starts loading where you left off.
- The buffer cache no longer throws away half-finished downloads when you pause or when the player asks for the segment just before them, which made it download the same segments twice.
- The wait for a head start before playback begins is at most 25 seconds.

## 0.3.5

- Right-click any title (or press the context menu key) for Resume, Play from beginning, Go to series, Add to My List, Add to playlist, Mark as watched or unwatched, Remove from Continue Watching, Like, Dislike and Not interested, plus Refresh metadata and Media info for library managers. Only the actions that fit the title are shown.
- My List, playlists and watch history (with single entries removable or the whole history cleared) on the new My List page. Everything belongs to your profile: resume points, watched state, likes, My List, playlists, history, the audio and subtitle tracks you pick for a movie or show, playback speed and quality.
- Recommendations follow your likes and dislikes and say why: "Recommended for you", "Because you liked …" and "More …" rows for the genres you watch. Not interested hides a title without counting against its genre.
- Customize home: pick and order the rows shown on the home page. Continue Watching now also shows the next episode of shows you are following, keeps one entry per show, and counts a title as finished once its credits start (configurable under Admin, Settings, Playback).
- Up Next: near the end of an episode a card counts down to the next one (Profile, Playback sets the countdown; 0 waits for a click). After a movie, related titles are offered.
- The player remembers your audio and subtitle choice for each movie and show, otherwise uses your preferred languages. Shows with several audio tracks get an Audio menu.
- Keyboard: Space or K play and pause, J, L and the arrows seek 10 seconds, Up and Down change the volume, M mutes, F fullscreen, C captions, I stats, N next episode, 0 to 9 jump through the title.
- Stats for nerds (press I or open it from the player settings): how the video is delivered, codec, resolution, bitrates, network speed, the buffer cache ahead and behind, cache size and hit rate, and dropped frames. The seek bar marks the parts stored on this device.
- The buffer cache sizes itself from the title's bitrate and the storage allowed (a low bitrate title keeps up to 20 minutes ahead), keeps to a per-title and a device-wide limit (Profile, Playback cache, with a Clear button), and drops the least recently watched titles first.
- Near the end of an episode, once it is stored to the end, the next episode's opening is downloaded too, so it starts at once.
- A stream that dies reopens at the same position up to three times before an error is shown.
- A device that fails to play a codec it claimed is not sent that codec again for 30 days (resettable under Profile, Playback cache).
- When a title is both local and on a media server, your profile chooses which plays first, and the other is used when the first is unavailable.
- Admin, Streams shows each session's profile, title, source, direct play or transcode, bitrate, position, buffer and network speed.

## 0.3.4

- The player never actually played from the Jellyfin buffer cache: it downloaded every segment again itself while the cache downloaded the same segments alongside it, so each segment crossed the network twice and playback still stalled. The player now reads from the cache, and a segment the cache is already downloading is shared instead of requested again.
- When Jellyfin sends the original video, the buffer cache downloads two segments at once, which on a fast link roughly doubles how far ahead of playback it gets. Re-encoded streams still download one segment at a time, because the encoder restarts when requests jump ahead.

## 0.3.3

- 4K Dolby Vision films from UHD Blu-ray (profile 7, with an enhancement layer) were re-encoded instead of sent as they are, which stalls on most Jellyfin servers. Jellyfin now sends their HDR10 picture untouched to devices with an HEVC 10-bit decoder, like other HDR films. Profile 5 Dolby Vision, which has no HDR10 picture, is still converted.
- Jellyfin titles start downloading into the browser the moment playback opens and keep the next ten minutes stored (and the last five), instead of five minutes and only once playing. Playback starts once enough is stored to run without stalling: a few seconds when the download is faster than the film, longer when it is not, at most a minute.
- Each Jellyfin stream's entry in the source's event log says what the file is and whether its video is sent as is or re-encoded, and why.

## 0.3.2

- Jellyfin movies no longer stall every few seconds. ViewDock asked Jellyfin to re-encode every non-H.264 file to H.264 at its full size, which for a 4K HEVC film is slower than real time. Each browser and device now reports which codecs it decodes, and whether its graphics hardware decodes them, and Jellyfin sends the original video (HEVC, AV1 or H.264, including 4K and HDR10) whenever the device can play it, converting only the audio. This works the same in the Discord Activity, per viewer.
- When a device cannot decode the original, Jellyfin re-encodes to H.264 at no more than 1080p and 20 Mbps for Auto quality, which Jellyfin keeps up with. If a browser claims a codec it then fails to play, the player switches to H.264 on its own at the same position.
- Pausing a Jellyfin title for more than 45 seconds ended the stream, so playing again failed with "started somewhere else". Paused and buffering players now keep their session.
- The playback buffer cache no longer downloads the same part twice when playback starts, and it keeps at most 4 GB (or half the browser's storage) per title, dropping what is furthest behind first.

## 0.3.1

- Jellyfin playback keeps the five minutes before and after the playhead stored in the browser, filled in the background from the moment playback starts, so a slow Jellyfin server or network no longer stalls the picture. Downloading pauses while the player is paused. The stored copy is kept for three minutes after the player closes, so resuming the same title soon reuses it, and is then deleted. Signing out deletes it at once.
- Jellyfin sources resync every 30 minutes instead of every 6 hours, so new, changed and removed titles show up sooner.
- Searching in the Discord Activity's title picker found nothing and always said "No matches". It now finds movies and shows, with their posters.

## 0.3.0

- Discord Activity: the first person in the voice channel to open it hosts and picks what everyone watches; the rest join that party automatically. An administrator who joins later takes over as host. The party panel has a Leave party button: after leaving you can watch on your own inside the Activity and Rejoin the channel's party from its start page.

## 0.2.9

- New home page in the style of Jellyfin: My Media tiles for Movies, Shows, Anime and All titles, then Continue Watching (wide cards with the episode as "S1:E5 - Title" and a progress bar), Next Up (the next unwatched episode of shows you are watching), and Recently Added rails for each section with previous and next buttons. Searching, filtering or opening All titles still shows the full grid with filters. Works with the sidebar open or collapsed and on phones.
- Jellyfin servers now also provide backdrop images, used by the home page's wide cards and tiles.
- ViewDock now creates each library's folder itself. Leave the folder empty and "Kids Movies" becomes `/media/Kids Movies`, owned by the ViewDock account and ready for uploads; no `mkdir`, `chown` or `chmod` over SSH. Library folders must be inside the media folder (or a `VD_LIBRARY_ROOTS` entry); `..`, links that lead elsewhere and folders used by another library are refused.
- On every start the container gives the ViewDock account any media folder that root created, such as a `movies` folder made over SSH, which caused "library folder is not writable". Files are never changed. Set `VD_FIX_PERMISSIONS=false` to turn this off.
- Move titles between libraries under Admin → Media: pick titles on the Titles page ("Move to library", "Move selected"), or move everything out of a library with "Move content". Movies go to Movies or Mixed libraries and shows to TV Shows or Mixed libraries, decided by each title's own kind, and ViewDock shows what will move and what will be skipped before you confirm. Files move on disk with their subtitles, artwork and show and season folders; watch history, progress, favourites, collections and metadata stay with each title. Nothing at the destination is ever replaced, a failed move puts the files back, and a move interrupted by a restart is finished or undone on the next start.
- Libraries now enforce their type: a Movies library does not pick up new episodes, a TV Shows library does not pick up new movies, and a library's type cannot be changed to one its titles do not fit.
- A library inside another library's folder is no longer scanned twice.
- The ViewDock process keeps the GPU and Docker socket groups after dropping root.
- The stream-limit error no longer blames the Jellyfin server. The limit is ViewDock's own "Maximum concurrent streams" setting for that server (Admin → Jellyfin servers), and every viewer counts, including each member of a watch party or Discord activity; the message now says so and how to raise it.

## 0.2.8

- Joining a watch party on a Jellyfin server limited to one stream could refuse your own playback with "allows 1 playback at once". When a party seek and a quality change started at the same moment, one of your streams blocked the other. Your overlapping starts now replace each other.
- After a failed start in a watch party, the party's position updates no longer keep reopening the stream. The player waits for Retry and resumes at the party's position.

## 0.2.7

- A Jellyfin server limited to one stream no longer blocks you with your own playback. Starting again replaces that stream, and a stream the player has not read for 20 seconds stops counting. Someone else's active stream still uses the slot.
- When that replacement stops the previous player, it stays stopped instead of opening the stream again.

## 0.2.6

- When a Jellyfin title fails to start, the player says why (the server could not be reached, it refused the stream, streaming or transcoding is off, or the stream limit is full) instead of only "the media source is unavailable". That reason is also written to the server log.
- Auto quality for Jellyfin now asks for the file's own bitrate. The previous request asked for up to 80 Mbps even when the file was much smaller, which can overload Jellyfin.
- A failed playback no longer starts again on its own. Pressing Retry repeatedly is ignored for a short moment, so a failure cannot hammer the media server.

## 0.2.5

- Fixed Jellyfin titles playing at low quality. ViewDock now tells Jellyfin the video bitrate to use, so Auto keeps the original quality; before, Jellyfin's encoder fell back to its own low default. The Quality menu also offers 1080p, 720p and 480p for Jellyfin titles when the source allows transcoding.
- The Server menu shows the Jellyfin server's name alone, without a "Jellyfin:" prefix.
- Movie and TV pages have a Back button and a "More like this" row of related titles. Closing the player returns to the title page without leaving the player in the back history.
- The header search and its filters only change the dropdown results. They no longer change the home page's filters or listing. Enter opens the top result, and long result lists can be expanded in the dropdown.

## 0.2.4

- Redesigned player controls. The quality box is replaced by a settings cog that opens upward with Quality, Server, Subtitles and Playback speed (0.25x to 1.5x). Previous and Skip are now 10 seconds back and forward buttons, also on the arrow keys.
- Subtitles can be picked in the player. Text subtitles are drawn by the player and switch off without restarting the stream; image subtitles are burned in. Text subtitles are now extracted as WebVTT.
- After 10 seconds paused, the player dims and shows what you're watching: title, season, episode and overview.
- Fixed a crash when a video reached its end, which also stopped the next episode from starting automatically. The play button no longer gets stuck on Play after moving to another episode.

## 0.2.3

- Player errors have a Details dropdown with a Copy button, and are reported to admins with a report ID. This covers startup failures and streams that stop during playback, which previously froze without a message.
- New Audit page in Admin: server and player errors with their details, plus the audit trail of admin actions. `GET /api/v1/admin/audit` and error reports are readable with a `logs.read` API key; the API key page has a copy button for new keys.
- Fixed Jellyfin titles that need transcoding never starting. The player now accepts Jellyfin's master playlist instead of waiting for segments in it until startup timed out.

## 0.2.2

- The home page filters sit in one toolbar: a media type switch, then Genre, Filters and Sort menus. The header search filter menu uses the same layout and is taller.
- Offline shows only device storage and the titles saved on this device. Movies and episodes are saved from their own page with Save offline.
- The account menu shows your Discord picture and @username. ViewDock refreshes them at each Discord sign-in and, when missing, looks them up with the bot.
- The Profile page is laid out in cards. Change password and PIN lock are hidden while Discord sign-in is on, because local sign-in is off; a PIN set earlier can still be removed.
- Scrollbars are no longer drawn anywhere; pages and lists still scroll. Dropdowns no longer clip their text.

## 0.2.1

- The home page lists every title, local and Jellyfin, with a grid or details layout, sorting, media type (Movies, TV shows, Anime), genre, and Recommended, Most viewed and Unwatched filters. Recommendations follow each account's watch history.
- The header search covers Jellyfin titles, suggests matches as you type, and has the same filters. The separate Search, Movies and TV pages are gone; old links open the matching home page view.
- The sidebar can collapse to icons, and the account button at its foot opens a menu with your Discord picture, Settings and Sign out.
- Every Settings category and Discord section is now its own page in their sidebars, instead of one long page.
- Titles store their genres from TMDB and Jellyfin; titles matched earlier get theirs in the background.

## 0.2.0

- Admin, Media brings together Libraries (add, edit, scan and delete local libraries), Titles (search every movie and series, match it on TMDB, upload a poster, set its rating), Uploads, Library access and Jellyfin servers.
- Media, Settings and Discord get their own sidebar in the admin area, with Back to admin at the top and their sections grouped below.
- Adding a Jellyfin server now uses a Save button. Each saved server has a What to sync box listing its Jellyfin libraries by type (Movies, TV shows, Anime, Mixed), to sync everything or only chosen libraries.
- Continue Watching shows titles and posters, and no longer shows items that were removed or that the viewer cannot access.

## 0.1.9

- Admins can connect Jellyfin servers under Admin, Media sources. Their movies and shows (with posters and metadata) appear for every user and resync every 6 hours. A title that is also in a local library is listed once, plays locally by default, and the player can switch to the Jellyfin copy. Jellyfin playback supports seeking and works in watch parties. Credentials stay on the server.
- Jellyfin sources can use an API key instead of an account. Each source has usage restrictions (posters, streaming, transcoding, activity log, maximum streams) that ViewDock enforces on itself, and a usage log of what ViewDock did with the source, plus optional Jellyfin activity for the ViewDock user.
- The Watch Together panel can be closed, and the host chooses whether it is shown to everyone, only the host, or nobody. A party button in the player controls reopens it.
- Admin, Watch parties lists web and Discord Activity parties. Admins can remove a member, block them from rejoining, end a party, and read each member's playback session and client logs.
- When a browser or Discord refuses to play a stream, the player now explains how to turn on hardware acceleration instead of showing a browser error.

## 0.1.8

- The close button in a watch party works inside the Discord Activity, and in a party opened from a link. In the Activity it returns to the title picker, with a Rejoin button while others are still watching.
- The Discord Activity no longer reopens the last movie when nobody is watching it; the channel picks a new title instead.

## 0.1.7

- The Discord Activity shows your movies and TV as the same poster grid as the home page, so you can pick a title without searching. Search narrows the grid.

## 0.1.6

- Registering slash commands globally adds the Discord Activity's launch command back when Activities are enabled but Discord has none, for example after an earlier registration removed it.

## 0.1.5

- Admin → Updates shows "Updated" as soon as the host finishes, instead of about two minutes later, and the log only shows the current run.
- Opening Admin replaces the main sidebar with the admin sidebar: **Back to app** first, then Overview and the admin pages in groups. Small screens get the same links as a scrollable bar.
- Every admin page uses the card grid layout from the Discord page. Overview is a dashboard with streams, users, libraries, node health and the version.
- Duplicate admin settings removed. Discord settings are only on the Discord page, not also under Settings. Resilience no longer repeats the live sessions shown under Streams. Library access (including the new per-person and per-group **Downloads** toggle) is edited only under Grants, and group membership only under Groups; Users shows both read-only with links.
- New **Discord Activity**: open ViewDock from a voice channel and the channel watches its party together, each person streaming with their own account. It reuses the sign-in application and bot, signs people in with the Discord account running the Activity under the usual sign-in rules, and is off by default. Setup steps are on the Discord page and in the Discord guide.
- Removed the Labs virtual camera, RTMP and SRT broadcaster and its admin page. Discord bots cannot send video, so watching inside Discord now uses the Activity. An upgrade deletes the saved Labs settings, including any stored output URL.

## 0.1.4

- The Discord bot now shows as online, watching movies & TV. ViewDock keeps a lightweight Gateway connection with the active bot token (shared or separate) that reconnects and resumes on its own. Slash commands, verification and party invites still use the HTTP interactions endpoint; the Gateway uses no privileged intents and handles no interactions.
- Admin → Discord diagnostics show the Gateway connection, presence and last connection separately from the HTTP bot and interactions checks, with a "Reconnect now" action and a clear message when Discord rejects the token.

## 0.1.3

- Admin → Updates no longer shows "Updating" when nothing is running. The host helper's systemd unit could create `update/request` as a directory, which looked like an update that never finished; it is removed, and an update the host does not pick up now fails after 2 minutes with a hint instead of waiting 30.
- The host helper no longer writes "no request" to `update/last.log` every few seconds; only real update runs are logged.
- Check now no longer overwrites the status of an update started while the check was running.
- Re-running the installer or `sudo viewdock update` pulls the image before stopping ViewDock, so a failed pull leaves the app running.
- The web app manifest is requested with credentials, so it loads behind Cloudflare Access.

## 0.1.2

- Admin → Discord is reorganised into cards with a status and diagnostics panel that checks credentials, sign-in, the bot's servers and permissions, slash commands and the interactions endpoint, with a fix for each problem.
- Optional "Use separate Discord bot configuration" for running the bot from a different Discord application. Off by default; existing settings keep working unchanged.
- Every `DEPLOY:` release bumps the patch version automatically, so Admin → Updates offers each new image.

## 0.1.1

- Updates page only reports an update when GitHub `VERSION` is newer than the installed version. Same version is up to date, even if the `:latest` digest changed.
- `sudo viewdock update` refreshes `install.sh`, pulls the image, and recreates the container. SQLite in `./config` and `./media` stay on disk.
- Superadmin role, user delete in Admin → Users, and Discord guild/role whitelist. Enabling Discord turns off all local login and signup. Set the Superadmin Discord user ID before enabling.

## 0.1.0

- First public image on `ghcr.io/skila1/viewdock`. Tags: `latest` (default branch), the `VERSION` file, git SHA, and semver from `v*` tags.
- One-line installer writes a Compose project in `./viewdock`, installs Docker if missing, and starts the stack. Optional Cloudflare Tunnel is a systemd service, not a container.
- Admin → Updates checks GHCR digest plus GitHub `VERSION` / `CHANGELOG.md`. Update now uses the host helper (`viewdock-update`) or the Docker socket.
- Automatic updates can pull a newer image about once an hour when the helper or socket is available.
- SQLite, one container. Media stays on your disk. First-run prints an 8-character setup token in the console and logs after the server is listening.
- `.env` only has `VD_PORT` and `VD_PUBLIC_URL`. Trusted proxies include local/Docker and Cloudflare. Public URL and TMDB are also under Admin → Settings.
- Administrators can upload videos (max 10 GB) under Admin → Uploads. Staging is `/config/uploads`. `/media` is read-write so finished files can land in the library.
- Admin → API keys issues `vd_…` bearer tokens. Admin → Logs stores application and playback events and is readable over the REST API.
- The player has an exit control. HLS waits for the first playlist instead of looping 410s.
