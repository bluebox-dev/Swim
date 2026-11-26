# Layout reference: the user's self-hosted NetBird dashboard (Control Center + Peers)

Observed read-only on 2026-11-25 (dashboard v2.99, dark theme, 1470 px wide). Described in words only;
no screenshots kept because the real page lists people's names and e-mail addresses.

## App shell
- One near-black surface (~#18191c) for both sidebar and content; no visible border between them. Top bar has
  no background of its own and a hairline bottom border.
- Top-left: logo mark + wordmark, then a small "collapse sidebar" icon button. Top-right: a round "?" help icon,
  a small outlined blue pill button (feature notice), and a round avatar with an initial.
- Sidebar (~210 px): items are icon (16 px, grey) + label (14 px, light grey); the active item sits on a
  rounded (8 px) slightly lighter grey block with white text. Section headings are tiny uppercase grey
  ("NETWORKING", "ACCOUNT") with generous space above. Expandable items show a chevron on the right; a small
  blue "Beta" badge sits after a label. At the bottom a rounded bordered box lists component versions
  (label left, version right, small text).

## Control Center (the home canvas)
- The whole content area is a canvas with a faint dotted grid (react-flow style), no cards around it.
- Top-left floating toolbar: a segmented control in a dark rounded container (Peer | User | Group | Networks),
  each with a small icon; the active segment has a lighter background.
- Top-right floating toolbar: segmented "● Live | ✎ Draft [Beta] ▾" — live mode with a green dot.
- Nodes: compact dark rounded cards (~220×52) with a square icon tile at left, bold name (truncated with
  ellipsis), grey secondary line (IP or "53 Peers"), and an up/down chevron on the left node to switch the
  focused item. Nodes are placed left and right with lots of empty canvas around them.
- Edges: a pair of thin parallel dashed GREEN lines (animated) from node to node, with a pill in the middle
  showing "● Default | All" (policy name + scope) on a dark background with a hairline border.
- Empty state (e.g. no networks): centred square icon tile, bold 24 px heading, two lines of grey text,
  orange primary button with a plus-in-circle icon, then a small "Learn more about X ↗" link in orange.
- Bottom-right floating "Feedback" button with an icon, outlined.

## List page (Peers)
- Large title with the count ("53 Peers"), one grey description line ending in an orange "Learn more ↗" link.
- Toolbar row: search input with placeholder "Search by name, IP, …" and a "⌘K" key hint inside; "Filters ⌄"
  button; a segmented filter (User Devices | Servers); an icon-only refresh button; primary orange "Add Peer ⌄"
  on the far right.
- Table: header band slightly lighter, small grey labels with sort icons; columns checkbox · NAME (green dot
  for online + bold name, grey second line) · ADDRESS (small flag/region icon + DNS name, grey IP under it) ·
  GROUPS (rounded chip with folder icon) · LAST SEEN (clock icon + "just now"/"3 days ago") · VERSION (agent
  version with an update icon, OS icon + OS name under it) · kebab menu button. Rows ~56 px, hairline dividers,
  no zebra.

## Colour and type
- Accent orange (~#f68330) only for primary buttons, links and the logo. Green for online/live and edges.
  Blue only for small notice pills/badges. Everything else greys on near-black. Sans-serif UI face; numbers
  and IPs in the same face, slightly smaller and grey. Radii ~8–10 px; outlines are hairlines; almost no shadows.
