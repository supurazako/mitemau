# Mitemau design guidelines

Use **Twitch and Kick for interaction patterns** and **Keynote for visual presentation**. A viewer should recognize where to find a live stream, start playback, and read chat. The visual hierarchy should feel like a presentation: one clear focal point, generous margins, and quiet supporting UI. This suits Mitemau's first use case: friends dropping in to watch, work, and chat. Use Mitemau's own colors, copy, and branding.

## Visual foundations

| Element | Guideline |
| --- | --- |
| Layout | Align the header and main content to the same centered grid. Start with 32 px desktop gutters and a 24 px gap between independent sections; use smaller gaps within a group. |
| Typography | Use a system sans-serif stack. Start with 32 px for page headings, 20 px for section headings, 16 px for body and controls, and 14 px for metadata. Use size, weight, and spacing to establish hierarchy. |
| Surfaces | Start with a neutral page background, quiet card surfaces, and a near-black player canvas. Use borders rather than heavy shadows to separate adjacent areas. |
| Color | Use one accent for links and primary actions. Reserve semantic colors for states such as Live and errors; accompany each with text. Ensure WCAG AA contrast for text and interactive controls, including on the player canvas. |
| Actions | Use a filled button for the main action in a section and quieter buttons for secondary actions. Label actions with verbs (`Watch camera`, `End stream for everyone`). Use an icon alone only for a familiar control with an accessible name. |

Keep page navigation short and consistent: `Live streams` for discovery and `Account & broadcasting` for account and sender controls. Mark the current page. Make keyboard focus visible, preserve a reading-order tab sequence, and keep focus stable when data refreshes. Use links for navigation and buttons for actions. At narrower desktop widths or with zoom, stack panels instead of clipping content or shrinking controls.

## Live list

Show a grid of stream cards with a shared 16:9 placeholder, stream title, participant or creator attribution, and a text `Live` badge. The title and thumbnail should lead to the same watch page as one link. Separate the creator link when it has a different destination. Wrap long titles and account identifiers without changing the card's layout. Don't imply an unavailable stream can be watched.

```text
Mitemau                                    Live streams · Account & broadcasting

Live streams
┌───────────────────────────┐    ┌───────────────────────────┐
│     Default thumbnail    │    │     Default thumbnail    │
└───────────────────────────┘    └───────────────────────────┘
Work and chat       • Live      Sketching           • Live
Creator …                       Creator …
```

## Watch page

Put the stream title and status first. On desktop, use a wide media column with chat alongside it; when space runs out, put chat after the media. Keep chat scrollback within the panel and keep the composer visible without making it cover messages. Signed-out visitors can watch and read chat; offer sign-in where they would otherwise compose.

Place participant attribution next to their media. Distinguish the camera and shared screen within each participant's section and label their controls separately. In a shared stream, additional media can sit below the first panel without pushing chat out of the desktop layout. An individual stopping or disconnecting should update only their section; keep other participants and chat visible. Keep playback explicit and preserve browser permission and audio-gesture behavior.

```text
Work and chat  • Live
┌──────────────────────────────────┐  ┌─────────────────────────┐
│ Participant A · Camera           │  │ Live chat               │
│ [Watch camera]                   │  │ Messages …              │
├──────────────────────────────────┤  │                         │
│ Participant A · Shared screen    │  │ Sign in to post         │
│ [Watch shared screen]            │  └─────────────────────────┘
└──────────────────────────────────┘
Participant B · Camera
Connection lost. Waiting for the participant to reconnect.
```

Keep report and moderation actions near the relevant person or comment, visually secondary to playback and posting. Add viewer volume, mute, and fullscreen as player controls when [#64](https://github.com/supurazako/mitemau/issues/64) lands. Place `Copy watch link` near the stream title and in broadcasting controls when [#65](https://github.com/supurazako/mitemau/issues/65) lands. Track viewer-selectable separate and inset layouts for camera and screen share in [#66](https://github.com/supurazako/mitemau/issues/66).

## States and feedback

| State | Example treatment |
| --- | --- |
| Initial load | Reserve the media or card area and show a short status such as `Loading live streams…`. Keep existing content in place during routine refreshes. |
| Empty list | `No streams are live right now.` Show this only after a successful load. |
| Offline participant page | `Offline` with a link back to `Live streams`. Keep the permanent participant page available. |
| Live, no media | `Not sharing` for a stopped sender; `Connection lost. Waiting for the participant to reconnect.` for a disconnected sender. Don't show a retry action that only the sender can perform. |
| Viewer playback failure | Explain the failure beside the affected player. Offer `Watch again` when playback can be retried by the viewer. |
| Request failure | Explain what failed beside the affected content and offer `Try again` when useful. Don't render an error as an empty list or as confirmation that a stream ended. |
| Stream ended | Show that the stream ended and offer a way back to discovery. Remove playback and public chat for that stream. |

Announce meaningful changes with a status region and use alerts for actionable errors. Avoid repeatedly announcing unchanged statuses. For forms, keep entered values on recoverable errors and show the reason when an action is unavailable.

## Availability

Render an action only when the current viewer can perform it. Do not show permission-gated controls as disabled placeholders: hide the entry point, or replace it with a short explanation and the next valid step.

- Signed-out visitors do not see report or invitation actions; comment posting shows a sign-in prompt.
- Broadcasters without approval see their application status instead of stream setup.
- Suspended accounts see the restriction reason instead of a disabled composer or sender.
- Viewers see delete and staff controls only when the server grants them.
- Public pages show invitation shortcuts only to approved broadcasters, and never for their own page.

State-gated controls, such as Connect before a live stream or Submit before required fields, may remain visible when the reason and next action are clear. Hiding a control is not authorization; every action is still checked by the server.

## Order of application

1. **Public:** apply the shared shell and live cards, then the watch layout, chat, and playback states. Review signed-out discovery, a single live stream, a shared stream, and empty, offline, and disconnected states at desktop widths.
2. **Account and broadcasting:** group sign-in and account setup separately from stream controls. Display the current stream state near the next valid action; keep ending everyone's stream visually distinct from stopping your own media or leaving. Keep source selection explicit when independent media modes land in [#67](https://github.com/supurazako/mitemau/issues/67). Refresh invitations without interrupting setup or sending ([#68](https://github.com/supurazako/mitemau/issues/68)).
3. **Staff:** reuse the same typography, inputs, and status patterns for report triage and account lookup. Identify the target and consequences next to moderation actions. Keep internal notes and reasons separate from messages shown to the affected account.

When reviewing a screen, verify that a first-time viewer can identify who's live, start watching, tell whose camera or screen is shown, and find chat using only the keyboard as well as with a pointer.
