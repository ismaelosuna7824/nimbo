# UX guidelines

Nimbo puts a lot on one screen: palette, canvas, inspector, a bottom dock with up to eight tabs and every run action.
These rules decide **where things live, what is visible when, and how loud it is**, so the app is easy to move around,
the screen reads calm and nothing looks broken when it isn't. No rule removes a capability: it moves it to where it
belongs.

Apply them to every new screen, panel, badge, button or message. The checklist at the end is the short version for
reviews.

## 0. One navigation, one place per control

Users should always know where they are and how to get anywhere in one click, and find each control in one place
([NN/g, Complex applications](https://www.nngroup.com/articles/complex-application-design)).

| Layer | Holds | Where |
| --- | --- | --- |
| **Rail** (left, every screen) | Places: Home, Learn, Connections, then the open workflows. Tools: Search (⌘K), theme, Settings (⌘,) | `features/nav/nav-rail.tsx` |
| **Page header** | The page's title and its one primary action (New workflow, New connection) | Each page |
| **Editor bar** | The workflow at a glance: name and save state; Guided/Pro, sandbox, Test, the last deploy's status, Deploy ▾, problems, workflow menu ⋯, inspector | `features/toolbar/top-bar.tsx` |
| **Menus** | Everything else: Validate/Plan/runs/Destroy under Deploy ▾; import, export, monitoring, convert, close under ⋯ | Same file |
| **⌘K palette** | Every action by name: a shortcut, never the only way | `features/command/command-palette.tsx` |

- The current place is marked in the rail (accent bar + `aria-current="page"`). Learn, Connections and Settings are
  pages over the screen underneath (the editor stays mounted): only one is open at a time (`keepOnePageOpen`), Escape
  closes it, and the rail goes anywhere from any of them; pages have no back buttons of their own.
- Workflows open like files in an IDE: every workflow opened stays in the rail (in opening order, kept across
  restarts, `features/toolbar/open-workflows.ts`) until it is closed on purpose (its × in the rail, ⌘W, the ⋯ menu).
  One is loaded in the editor at a time; picking another saves the current one and switches; closing the active one
  shows its neighbor, or Home after the last. Moving around never closes a workflow: Home from the rail is a page
  over the editor too, and Home marks the loaded workflow's card "Open".
- Global controls (theme, Settings, Learn, search) are never repeated in headers or the editor bar.
- Palette categories (every cloud, Docker and Kubernetes) carry an icon on a soft tint from one shared map
  (`components/category-icon.tsx`); related categories share a tint, and tints never use red, amber or green, which
  are reserved for status.

## 1. Show what the current step needs; one click for the rest

Progressive disclosure: the default view carries the essentials, advanced or rarely used things sit one predictable
click away (a tab, a menu, a "Pro" toggle). Never remove a capability to reduce clutter; defer it.
([NN/g, Progressive disclosure](https://www.nngroup.com/articles/progressive-disclosure/);
[NN/g, Complex applications](https://www.nngroup.com/articles/complex-application-design))

- **Guided** is the disclosure lever. Guided shows plain-language help, recommended connections applied for you and
  read-only code; **Pro** shows every attribute, expressions and editing.
- Guided opens a workflow with the bottom dock collapsed to its tab strip: the canvas is the task. Every tab is still
  one click (or ⌘J) away. Pro opens with the dock up, and switching to Pro brings it up; switching to Guided never
  yanks away a panel the user opened.
- A collapsed area keeps its labels visible (the tab strip stays), so nothing becomes undiscoverable.
- **Inspector** (every workspace kind, `features/inspector/inspector-section.tsx`): header, then status callouts, then
  collapsible sections in a fixed order: Connect (deployed values; identifiers shown once, every attribute behind
  "All attributes") → Settings → Access / Network / Plan → the rest. Each section remembers open/closed per section
  type (localStorage); Advanced starts closed. Once the body scrolls with three or more sections, a sticky chip row
  (`nav`, `aria-current`) jumps between them. Long forms are grouped by purpose: Terraform Settings keep Required
  first, then General, Networking, Security & encryption, Capacity & scaling, Storage & data, Logging & monitoring,
  Backup & protection, Tags & labels, Advanced (`settings-groups.ts` documents the precedence), set settings first in
  each group; resources with more than 12 settings switch between All and Configured (set + required + "Add
  setting…"). Docker services and Kubernetes workloads group their fields the same way.

## 2. Red means broken; amber means needs work; zero is silent

Status color is reserved for status, and its strength matches the severity
([Carbon, Status indicators](https://carbondesignsystem.com/patterns/status-indicator-pattern)).
Overusing red trains people to ignore it, and makes a healthy workflow look like an incident.

| Signal | Use for | Never for |
| --- | --- | --- |
| Red (`text-destructive`) | A failed check, an error that blocks a deploy | Low scores without failures, untested items, counts of 0 |
| Amber | A warning, something that needs work | Difficulty levels, neutral information |
| Violet | Not tested yet / pending evidence | Problems |
| Muted | Nothing to report | — |

- Derive the tone from the **verdict**, not from a number: a readiness category with one warning scores 50 and is
  amber, not red (`scoreTone` in `features/readiness/status.ts`).
- Counts of zero are not shown: the Problems button in the toolbar is an icon with a "No problems" tooltip until there
  is something to look at; the Problems tab's badge only appears with alerts.
- Containers that always exist (tabs, section headers) use neutral icons. The warning sign belongs on the problem,
  not on the place where problems would be listed.
- Compliance (`features/compliance/status.ts`): green only for controls Nimbo verified on the design (met), red only
  for violations (a finding on any resource a control applies to, shown as "N of M"), violet for manual
  controls only the user can attest (dashed while open, solid once attested: their evidence is the user's word, not
  Nimbo's), muted for not applicable / not evaluated with the reason in a tooltip.

## 3. Keep destructive actions away from the primary one

Destroy, Down and Delete never sit next to Deploy/Apply with the same look. Separate them by distance and a divider,
give them a redundant signal (red on hover or red text) and keep the confirmation dialog.
([NN/g, Consequential options close to benign options](https://www.nngroup.com/articles/proximity-consequential-options))

- Toolbar: Destroy is the last item of the Deploy menu, under a divider, in red, and asks for confirmation.
- An icon that clears a value (`X`) must not look like the icon that deletes the row (`Trash2`).

## 4. One primary action per region

Each region has at most one filled (primary) button: Deploy in the toolbar, Apply/Up/Deploy in the Deploy tab, the
main call to action in an empty state. Everything else is `outline`, `secondary` or `ghost`.

## 5. Open on something meaningful

Empty states explain what will appear and offer one action
([NN/g, Empty states in complex applications](https://www.nngroup.com/articles/empty-state-interface-design)). When
a view opens with content, pick the most relevant item, not the first in sort order (the Code tab opens `main.tf` or
the first Terraform file, never `.gitignore`).

## 6. Don't interrupt; never stack windows

A modal opens only in response to the user, or when nothing else is open. Automatic prompts (setup wizard, update
notices) wait until the dialog the user is in is closed (`whenNoDialog` in `features/onboarding/wizard.tsx`).

## 7. Icon-only buttons need a name

Every icon-only button has an `aria-label` and a tooltip with the same text (and its shortcut, if any). Primary
actions keep a visible label.

## Visual system

Tokens live in `apps/desktop/src/index.css`; shared canvas card styles in `components/canvas-card.tsx`. Use them
instead of one-off colors, shadows or radii.

- **Window frame (desktop app, every OS):** no native title bar. A 38px strip in the rail's color
  (`features/nav/title-bar.tsx`) makes rail and title bar read as one frame around the content, which is an inset
  panel with a rounded top-left corner. On macOS the traffic lights float over its left end; on Windows and Linux the
  window is undecorated (`tauri.windows.conf.json`, `tauri.linux.conf.json`) and the strip draws minimize, maximize
  and close on its right end. The strip holds the command bar: it names the active workspace (its icon and title, or
  the page shown) and opens ⌘K / Ctrl K (the rail's Search item is hidden there); the strip drags the window,
  maximizes on double click and stays usable while a modal dialog is open. Full-window layers outside the app frame
  start below it (`--app-top`). The browser keeps its own frame and the rail's Search item.

- **Surfaces.** Neutrals carry a faint violet-blue tint. Lowest to highest: rail (`bg-rail`) < canvas (`bg-canvas`)
  < pages and panels (`bg-background`, `bg-sidebar`) < cards (`bg-card`) < popovers and dialogs (`bg-popover`).
- **Elevation.** Three shadow levels, light and dark: `shadow-xs`/`shadow-sm` for resting controls and cards,
  `shadow-md` for hovered cards and floating canvas chrome, `shadow-lg` for popovers and dialogs. Floating canvas
  chrome (view switch, Tidy up, zoom, minimap) is frosted glass: `bg-card/80 backdrop-blur-md shadow-md` and a border.
- **Radius.** `--radius` is 0.75rem: `rounded-lg` for controls, `rounded-xl` for cards, `rounded-2xl` for frames.
- **Status dot** (`StatusDot`): one per card, its meaning in the tooltip and accessible name. Filled green = deployed
  or running; filled amber (pulsing) = starting; filled red = failed; hollow green = runs locally, not deployed yet;
  hollow gray = export only or stopped. Error and warning counts stay as their own badges (rule 2).
- **Selection and focus.** A selected card gets the accent border, a ring and a soft glow (`CARD_SELECTED`); keyboard
  focus always shows `ring-ring/50`.
- **Motion.** 150 ms on color, shadow and border; nothing moves layout. Edges between running ends drift slowly
  (`edge-flow`). With `prefers-reduced-motion`, transitions are instant and the flow stands still.
- **Spotlight on the canvas** (Follow a request, reachability, Compliance "Show on canvas"): the items that matter
  are ringed, the rest fade (`opacity-35`/`-25`); nothing in the workflow changes. The
  walkthrough rings the current item in the accent (focus, not status), keeps the rest of its path at 60 % and
  explains each step in one compact floating panel at the bottom center (frosted, `shadow-lg`): one row with the
  counter and Play/Pause, ←/→, speed and close, then a short header and a few one-line rows, cut short with the
  whole text in a tooltip. The camera eases to the hop above the panel, never below a zoom where card text stays
  legible (it pans instead), and jumps with reduced motion. Floating panels never stack: the caption and the live
  traffic panel share the canvas bottom and the newer one shows (the minimap steps aside meanwhile).
- **Give the canvas the room.** A walkthrough or tour runs on the Build canvas only and collapses the dock, the palette
  and the inspector while it runs, then restores exactly what was open when it closes (Esc, ×, switching to
  Architecture).
- **Guided tour.** The Build toolbar has one *Guided tour* split button: the button tours the infrastructure, its menu
  also holds *Follow a request* (disabled with the reason when nothing reaches the design). The tour's focus is a soft
  accent glow breathing slowly around the card(s) (`tour-glow`, still with reduced motion), never a status color; what
  was just shown keeps a faint accent ring; plumbing is named on the step ("Also created here"), not toured. Small
  sibling groups are toured card by card; a large one gets an overview, then sub-steps (2a, 2b…) at a readable zoom.
  A request path is never network plumbing: gateways and subnets are "passes through" context on the step after
  them, a path ends on a component in plain words ("It ended at EKS Node Group workers"). When nothing receives
  requests, the panel says why for this design and what to add, calm (no warning color), with the useful
  alternatives as buttons (an operator's kubectl path to a public cluster, *Tour the infrastructure instead*).

## Review checklist

- [ ] Does it fit the one navigation (rail for places, bar for the workflow, menus for the rest), without a second
      way to get somewhere or a second copy of a global control?
- [ ] Is the new element needed for the current step? If not, is it behind a tab, menu, Pro mode or a collapsed area?
- [ ] Does red only appear for something that is actually broken? Amber only for warnings?
- [ ] Are zero counts and "nothing here" states quiet (muted, no badge)?
- [ ] Is any destructive action separated from the primary action and confirmed?
- [ ] Is there at most one primary button in the region?
- [ ] Does the view open on the most relevant item, with an empty state that says what to do?
- [ ] Can it open on its own over another dialog? It must wait instead.
- [ ] Do icon-only buttons have an `aria-label` and a tooltip?
