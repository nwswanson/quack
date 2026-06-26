# Warm Light Admin UI Specification

Vendor-neutral guidance for a high-visibility maintenance interface.

## Purpose

The **Warm Light** interface style is intended for administration and maintenance tools: places where an operator logs in to change settings, manage users, edit policies, review logs, administer hardware, rotate secrets, run backups, or perform controlled operational fixes.

This is **not** a dashboard style. It should not emphasize passive observation, large KPI tiles, hero charts, or decorative status displays. The interface should feel like a well-organized toolbox: calm, legible, predictable, dense where necessary, and safe to operate.

The “cozy” quality comes from warm neutral surfaces, restrained amber accents, soft separation, and a calm visual rhythm. It should not come from overt cozy imagery such as plants, coffee cups, mascots, illustrations, cabins, stickers, or whimsical UI affordances.

## Design Principles

### 1. Maintenance first

The interface exists to help administrators **do things**:

- edit configuration
- create and modify policies
- administer users and roles
- inspect audit trails
- review logs
- run maintenance tasks
- recover from operational problems
- make deliberate high-impact changes

Avoid layouts that imply the main job is watching metrics. Charts may exist when useful, but they should be secondary to forms, tables, editors, controls, logs, and action history.

### 2. Dense but controlled

Administration interfaces often require dense information. The design should support that without becoming noisy.

Use:

- persistent navigation
- strong page titles
- clear section grouping
- predictable form layouts
- readable tables
- contextual side panels
- explicit save and discard actions
- inline validation
- clear status labels

Avoid:

- decorative cards without operational purpose
- overly large whitespace that forces excessive scrolling
- too many accent colors
- hidden primary actions
- ambiguous icon-only controls
- dashboard-style hero areas

### 3. Calm, warm, and high contrast

Warmth must never reduce usability. Primary text, labels, table content, field values, errors, and focus states must remain highly visible.

The palette should create a sense of warmth through:

- cream backgrounds
- warm off-white surfaces
- muted beige sidebars
- soft tan borders
- dark warm text
- amber accents used sparingly

The interface should feel approachable, not cute.

---

## Layout Architecture

Use a consistent application shell across admin screens.

### Desktop structure

Recommended structure:

```text
┌──────────────────────────────────────────────────────────────┐
│ Top utility bar                                               │
├──────────────┬────────────────────────────────────┬──────────┤
│              │ Content header                     │          │
│              ├────────────────────────────────────┤          │
│ Left sidebar │ Tab row / local navigation          │ Right    │
│              ├────────────────────────────────────┤ rail     │
│              │ Primary work area                   │ optional │
│              │ Forms, tables, editors, logs        │          │
└──────────────┴────────────────────────────────────┴──────────┘
```

### Recommended dimensions

| Element | Recommended size |
|---|---:|
| Left sidebar | 240–280 px |
| Collapsed sidebar | 64–72 px |
| Top utility bar | 56–64 px high |
| Content max width | 1280–1440 px on desktop |
| Optional right rail | 280–320 px |
| Card/content padding | 20–24 px |
| Page outer padding | 24–32 px |
| Primary content grid | 12 columns with gutters |

These are starting points, not hard requirements. The important rule is that pages should feel predictable and aligned across settings, policy, user, log, and maintenance screens.

### Page regions

#### Top utility bar

Use the top utility bar for global utilities only:

- global search
- notifications
- help
- current user menu
- environment or instance indicator
- optional global command launcher

Do not overload the top bar with page-specific controls unless the product is very small.

#### Left sidebar

The sidebar establishes location and task context. It should remain stable across the application.

Good section groups:

- **Manage**
  - Users
  - Roles
  - Groups
  - Permissions
  - Policies
- **Configure**
  - Settings
  - Integrations
  - Secrets
  - Hardware
  - Backups
- **Operations**
  - Logs
  - Tasks
  - Maintenance
  - Audit log

#### Content header

The content header should answer:

1. What am I editing or viewing?
2. What state is it in?
3. What are the primary actions?

Typical content header contents:

- page title
- short description or metadata
- primary action
- secondary actions
- optional status badge
- optional breadcrumbs for deep areas

Example:

```text
Edit Policy: File Upload Restrictions        [Duplicate] [Preview] [Save Changes]
Rules are evaluated from top to bottom.
```

#### Tab row

Use tabs to divide complex maintenance tasks into stable sub-areas.

Examples:

```text
Summary | Rules | Exceptions | Auditing
Profile | Roles | Groups | API Keys | Sessions | Audit Log
System | Security | Notifications | Network | Advanced
```

Tabs should represent peer sections of the same object or workflow. Avoid using tabs as arbitrary space-saving containers.

#### Primary work area

The primary work area holds the main task:

- forms
- rule builders
- configuration editors
- user tables
- role editors
- logs
- backup controls
- maintenance actions

This area should carry the strongest hierarchy after the page title.

#### Right rail

The optional right rail should contain contextual support, not the main workflow.

Good right rail content:

- object status
- metadata
- enforcement state
- recent changes
- related tasks
- audit snippets
- linked documentation
- warnings
- dependency notes

Avoid placing required editing controls in the right rail unless the entire screen is designed around a split editor pattern.

---

## Ordering and Hierarchy

Use a predictable visual order:

1. Persistent navigation
2. Page or object title
3. Primary and secondary actions
4. Local tabs or section navigation
5. Main form, table, editor, or log
6. Supporting panels and metadata
7. Destructive or high-risk actions

### Primary actions

Primary actions should be easy to find and consistently placed.

Recommended patterns:

- Put the main action near the page title on initial pages.
- Pin save/discard actions at the bottom of long edit forms.
- Use one primary action per section whenever possible.
- Keep secondary actions visually quieter.

Examples:

```text
[Save Changes]         primary
[Preview]              secondary
[Duplicate]            secondary
[Discard]              secondary/destructive-adjacent
[Delete Policy]        destructive
```

### Destructive actions

Destructive or high-risk actions should be visually separated from normal actions.

Examples:

- delete user
- delete policy
- wipe data
- rotate encryption keys
- restart services
- revoke all sessions
- disable SSO
- remove hardware binding

Recommended treatment:

- Place lower on the page or in a clearly labeled “Danger zone.”
- Use muted danger colors until interaction is required.
- Require confirmation for irreversible actions.
- Use exact, audit-friendly wording.
- Avoid placing destructive buttons beside routine save buttons.

---

## Spacing and Sizing

Use a small spacing scale consistently.

### Spacing scale

| Token | Size | Typical use |
|---|---:|---|
| `space-1` | 4 px | icon alignment, compact internal gaps |
| `space-2` | 8 px | label-to-field gap, small control gaps |
| `space-3` | 12 px | compact row spacing, inline groups |
| `space-4` | 16 px | related controls, list item padding |
| `space-5` | 20 px | compact card padding |
| `space-6` | 24 px | standard card/content padding |
| `space-8` | 32 px | major section spacing |
| `space-10` | 40 px | page-level separation |

### General spacing rules

- Use **8 px** between a label and its field.
- Use **16 px** between related controls.
- Use **20–24 px** inside cards and panels.
- Use **24–32 px** between major sections.
- Use **4–8 px** for icon/text alignment.
- Keep dense tables compact, but not cramped.
- Prefer consistent spacing over mathematically perfect spacing.

### Interactive sizing

| Element | Recommended size |
|---|---:|
| Text input height | 36–44 px |
| Select height | 36–44 px |
| Button height | 36–44 px |
| Compact table row | 40 px |
| Standard table row | 44–48 px |
| Icon size | 16–20 px |
| Checkbox/radio visual | 16–18 px |
| Toggle height | 20–24 px |
| Card radius | 8–12 px |
| Control radius | 6–10 px |

---

## Color System

The color system should use warm neutrals as the foundation and amber as the restrained action/focus color.

### Core palette

| Role | Hex | Usage |
|---|---|---|
| Background | `#F6F2EA` | application background |
| Surface | `#FFFDF9` | cards, forms, main panels |
| Sidebar surface | `#F1E8DA` | sidebar and quiet navigation areas |
| Elevated surface | `#FFFFFF` | overlays, menus, modals |
| Border | `#DDD2C2` | card borders, dividers, field borders |
| Border subtle | `#E8DED0` | low-emphasis dividers |
| Primary text | `#2F261F` | headings, primary body text |
| Secondary text | `#6E6258` | helper text, metadata |
| Muted text | `#8A7C70` | timestamps, secondary table data |
| Accent amber | `#C98A2E` | primary actions, active tabs, selected nav |
| Accent amber hover | `#B67922` | hover/active state for amber controls |
| Accent amber soft | `#F5E6CC` | selected sidebar background, badges |
| Focus ring | `#D99A3D` | keyboard focus and active field emphasis |
| Success | `#4E8B62` | success state, active status |
| Success soft | `#E6F2EA` | success badges |
| Warning | `#B97A1E` | warning state |
| Warning soft | `#F7E9CC` | warning badges |
| Danger | `#B6523A` | destructive actions, errors |
| Danger soft | `#F8E2DD` | error backgrounds |
| Info/link | `#5E7FA3` | informational links and low-risk navigation |

### Color usage rules

- Use amber sparingly.
- Do not make every icon amber.
- Use amber for selected navigation, active tabs, primary actions, and focus emphasis.
- Use semantic colors only for semantic states.
- Keep large backgrounds neutral.
- Keep text contrast strong.
- Do not use low-contrast beige text on beige surfaces.
- Use soft semantic backgrounds for badges and inline messages.
- Use stronger semantic colors for text, borders, or icons inside those messages.

### Suggested CSS custom properties

These names are optional and framework-neutral.

```css
:root {
  --color-bg: #F6F2EA;
  --color-surface: #FFFDF9;
  --color-surface-elevated: #FFFFFF;
  --color-sidebar: #F1E8DA;

  --color-border: #DDD2C2;
  --color-border-subtle: #E8DED0;

  --color-text: #2F261F;
  --color-text-secondary: #6E6258;
  --color-text-muted: #8A7C70;

  --color-accent: #C98A2E;
  --color-accent-hover: #B67922;
  --color-accent-soft: #F5E6CC;
  --color-focus: #D99A3D;

  --color-success: #4E8B62;
  --color-success-soft: #E6F2EA;
  --color-warning: #B97A1E;
  --color-warning-soft: #F7E9CC;
  --color-danger: #B6523A;
  --color-danger-soft: #F8E2DD;
  --color-info: #5E7FA3;
}
```

---

## Typography

Use a modern, neutral sans-serif UI typeface. The exact font is not important; the behavior is.

The typeface should have:

- clear numerals
- readable punctuation
- strong distinction between similar characters
- good rendering at 12–16 px
- support for tabular numbers where useful
- enough weights for regular, medium, and semibold

### Type scale

| Role | Size | Weight | Notes |
|---|---:|---:|---|
| Page title | 28–32 px | Semibold | clear task or object name |
| Section title | 20–24 px | Semibold | major page regions |
| Card title | 16–18 px | Semibold | panels and grouped content |
| Table header | 12–13 px | Medium/Semibold | compact and scannable |
| Body text | 14–16 px | Regular | primary reading text |
| Form label | 12–13 px | Medium | above fields |
| Metadata | 12–13 px | Regular/Medium | timestamps, IDs, secondary details |
| Helper text | 12–13 px | Regular | under fields |
| Button text | 13–14 px | Medium | action labels |

### Typography rules

- Use sentence case for headings, labels, and buttons.
- Avoid all-caps except for small sidebar group labels.
- Use line-height between **1.4 and 1.6** for paragraphs.
- Keep line length around **60–80 characters** for long text.
- Use tabular numbers in logs, tables, and timestamps if supported.
- Do not rely on font weight alone to communicate state.

---

## Sidebar and Navigation

The sidebar is the main orientation device.

### Structure

Use grouped navigation with quiet section labels.

Example:

```text
Manage
  Users
  Roles
  Groups
  Policies
  Permissions

Configure
  Settings
  Integrations
  Secrets
  Hardware
  Backups

Operations
  Logs
  Tasks
  Maintenance
  Audit log
```

### Navigation item anatomy

Each item should include:

- icon
- text label
- optional badge or count
- selected state
- hover state
- focus state

### Sidebar states

#### Default

- warm sidebar background
- dark text
- low-emphasis icons
- selected item with soft amber background
- selected icon/text with stronger contrast

#### Hover

- slightly stronger warm surface
- do not overuse amber

#### Selected

- soft amber background
- optional amber left rule or icon
- clear text weight increase

#### Collapsed

- 64–72 px wide
- icons only
- tooltips on hover/focus
- preserve selected state
- provide an obvious expand control

### Sidebar rules

- Keep navigation labels short.
- Do not hide major sections behind nested menus unless necessary.
- Avoid more than two levels of navigation.
- Use badges only when they help action.
- Separate management, configuration, and operations.

---

## Forms and Controls

Forms are central to this interface style. They should be clear, predictable, and forgiving.

### Labels

Prefer labels above fields.

```text
Server name
[ production-api-01 ]
```

This works better than left-aligned labels for narrow screens and long settings names. Use left-aligned labels only for highly structured, repeated property grids where comparison is more important than editing speed.

### Field grouping

Group related settings into panels or sections.

Example:

```text
Authentication
  Authentication mode
  SSO provider
  SSO issuer URL
  Require MFA for admins
  Allow local accounts

Session policy
  Session timeout
  Idle timeout
  Re-authentication interval
```

### Form layout

Use:

- one-column forms for complex or high-risk settings
- two-column forms for short, comparable fields
- inline controls only when the relationship is obvious
- clear section titles
- helper text for risk, scope, or side effects
- inline validation close to the field

Avoid:

- very wide text fields
- unlabeled toggles
- long horizontal forms
- hiding defaults
- placing save actions far from the edit context
- making destructive actions look like ordinary form actions

### Inputs

Recommended behavior:

- default height: 36–44 px
- label above field
- helper text below field
- error text below helper text or replacing helper text
- visible focus ring
- disabled state that remains readable
- placeholder text only as an example, never as the sole label

### Selects

Use selects for bounded choices.

Good examples:

```text
Authentication mode
[ Local + SSO ▼ ]

Session timeout
[ 12 hours ▼ ]
```

Avoid selects with very large option sets. Use searchable pickers or autocomplete when choices exceed a reasonable scan length.

### Toggles

Use toggles for immediate binary settings that read naturally as on/off.

Good:

```text
Require MFA for admins     [on]
Allow local accounts       [on]
```

Avoid toggles when the action is destructive, delayed, or requires confirmation. In those cases, use a button plus confirmation.

### Checkboxes

Use checkboxes for independent choices.

Good:

```text
Applies to roles
[x] Administrator
[x] Operator
[ ] Viewer
```

### Radio groups

Use radio groups for mutually exclusive options when all options should be visible.

Good:

```text
Effect
(o) Allow
( ) Deny
```

### Tags and tokens

Use tags for small sets of selected values.

Example:

```text
File extensions
[ .exe × ] [ .bat × ] [ .cmd × ]
```

Tags should be easy to remove, keyboard-accessible, and readable at small sizes.

### Buttons

Button hierarchy:

| Type | Usage |
|---|---|
| Primary | save, create, apply, confirm routine action |
| Secondary | preview, duplicate, import, export |
| Tertiary/link | low-emphasis navigation or related action |
| Destructive | delete, revoke, wipe, rotate high-impact key |

Rules:

- Use one primary button per local decision area.
- Do not use multiple amber buttons in the same cluster unless one is clearly dominant.
- Keep destructive actions visually distinct.
- Prefer clear verbs: “Save changes,” “Create user,” “Preview policy.”
- Avoid vague labels like “Submit” or “OK.”

### Save and discard

For long edit pages, use a consistent save area.

Recommended:

```text
Unsaved changes                                  [Discard] [Save Changes]
```

This can be sticky at the bottom of the viewport if the page is long.

### Validation

Validation should be immediate, local, and specific.

Example:

```text
Max login attempts
[ -1 ]

Must be a positive number.
```

Rules:

- Put error text close to the field.
- Preserve the user’s input when showing errors.
- Explain how to fix the problem.
- Use color plus text, not color alone.
- Summarize multiple errors at the top only when necessary.
- Do not block navigation without clearly explaining unsaved changes.

---

## Tables and Dense Information

Tables are the workhorse pattern for this UI.

Use tables for:

- users
- roles
- policies
- audit logs
- system events
- hardware bindings
- backup history
- active sessions
- API keys
- permissions

### Table structure

Recommended order:

1. table title or section title
2. short description if needed
3. filters and search
4. bulk action toolbar
5. table
6. pagination
7. optional selected-row detail panel

### Table sizing

| Element | Recommended size |
|---|---:|
| Header row | 36–40 px |
| Compact row | 40 px |
| Standard row | 44–48 px |
| Cell horizontal padding | 12–16 px |
| Cell vertical padding | 8–12 px |

### Table rules

- Use strong column labels.
- Align predictable data types consistently.
- Keep status indicators readable.
- Use muted metadata for timestamps and IDs.
- Keep row actions consistent.
- Use hover states to guide scanning.
- Consider sticky headers for long tables.
- Put filters above the table.
- Put pagination below the table.
- Use bulk actions only after selection.
- Avoid hiding critical state behind icon-only cells.

### Status in tables

Good status examples:

```text
● Active
● Pending
● Draft
● Failed
```

Use both shape/text and color. Do not rely on color alone.

### Audit log table

Audit logs should be optimized for traceability.

Recommended columns:

```text
Time | Actor | Action | Resource | Result | Details
```

Include expandable details when needed:

```text
Time:      May 15, 2024 2:41:33 PM
Actor:     admin
IP:        10.0.12.45
Action:    Updated policy
Resource:  File Upload Restrictions
Result:    Success
Details:   Added rule “Allow Images”
```

---

## Cards, Panels, and Right Rails

Cards and panels should organize context. They should not become decorative clutter.

### Good card uses

- policy status
- enforcement status
- recent changes
- related tasks
- selected user metadata
- backup summary
- hardware connection state
- warning summaries
- active sessions
- small audit snippets

### Card style

Recommended:

- surface: warm off-white
- border: subtle tan
- radius: 8–12 px
- padding: 20–24 px
- shadow: very subtle, optional
- title: 16–18 px semibold
- body: 13–14 px

### Right rail rules

The right rail should answer:

- What is the state of this object?
- Who last changed it?
- What related tasks are available?
- Are there warnings or dependencies?
- Where can I inspect audit history?

It should not contain the main editing form unless the page is specifically a split-pane workflow.

---

## Policy Editor Pattern

A policy editor is a canonical Warm Light screen because it combines dense data, risk, forms, and auditability.

### Recommended layout

```text
Page title: Edit Policy: File Upload Restrictions
Tabs: Summary | Rules | Exceptions | Auditing

Main area:
  Policy rules table
  Rule detail editor
  Validation messages

Right rail:
  Policy status
  Enforcement state
  Recent changes
  Related tasks

Bottom:
  Unsaved changes bar
  Discard
  Save changes
```

### Rule table

Recommended columns:

```text
Order | Name | Conditions | Effect | Actions
```

Rules should make evaluation order obvious. If order matters, support drag handles or explicit move controls.

### Rule detail editor

Use a structured builder:

```text
When
[ File type ▼ ]

Operator
[ is one of ▼ ]

Values
[ .exe × ] [ .bat × ] [ .cmd × ]

Effect
(o) Allow
( ) Deny
```

### Validation

Policy validation should be prominent but not alarming unless the policy is unsafe.

Examples:

```text
No issues found.
Rule will never match because a broader rule appears above it.
Default action is deny.
Policy is disabled and will not be enforced.
```

### Auditability

Every policy screen should expose:

- created date
- last modified date
- last modified by
- enforcement scope
- recent changes
- link to full audit log

---

## User Administration Pattern

User administration pages should support quick scanning and safe editing.

### User table columns

Recommended:

```text
User | Email | Role | Status | Last active | Actions
```

Optional:

```text
MFA | Groups | Created | Login method
```

### User detail layout

Use tabs:

```text
Profile | Roles | Groups | API keys | Sessions | Audit log
```

Good right rail content:

- account status
- MFA status
- last login
- created date
- risky permissions
- recent changes
- active sessions

### Safety rules

- Require confirmation for disabling users.
- Require confirmation for revoking all sessions.
- Make service accounts visually distinct.
- Show when credentials or API keys were last used.
- Never reveal secret values after creation.

---

## Settings Pattern

Settings pages should be organized around conceptual groups, not implementation internals.

Good groups:

```text
Authentication
Password policy
Session policy
API access
TLS / certificates
Audit logging
Network
Email / SMTP
Backups
```

### Settings layout

Recommended:

```text
Settings
  System | Security | Notifications | Network | Advanced

Security
  Authentication
  Password policy
  Session policy
  API access
  TLS / certificates
  Audit logging
```

Use a secondary vertical sub-navigation inside complex settings pages when there are many sections.

### Setting descriptions

Every high-impact setting should explain:

- what it changes
- who it affects
- when it takes effect
- whether it requires restart
- whether it is audited
- whether it can lock out administrators

Example:

```text
Require MFA for admins
When enabled, administrators must complete multi-factor authentication
before accessing management screens. Existing sessions are not interrupted.
```

---

## Maintenance and Danger Zone Pattern

Maintenance screens must make risk obvious without making the whole product feel alarming.

### Maintenance action examples

- restart services
- rebuild search index
- clear cache
- rotate encryption keys
- run diagnostics
- export configuration
- restore backup
- wipe system data

### Danger zone rules

- Use a dedicated section.
- Use descriptive text.
- Separate each action into its own row or card.
- Use danger styling only for genuinely risky actions.
- Require confirmation for irreversible or disruptive actions.
- Show expected impact.
- Show whether the action is audit logged.
- Show whether the action runs immediately or schedules a task.

Example:

```text
Rotate encryption keys
Rotates active encryption keys. Services may need to restart after rotation.
This action is audit logged.

[Rotate Keys]
```

For irreversible actions:

```text
Wipe system data
Permanently deletes all system data. This action cannot be undone.

[Wipe System Data]
```

Require a confirmation phrase for extreme actions.

---

## Modals and Confirmations

Use modals for actions that need focused confirmation, not for routine editing.

### Confirmation modal structure

```text
Title: Delete policy?

Body:
This will permanently delete the policy “File Upload Restrictions.”
Requests currently governed by this policy may fall back to the default policy.

Details:
- This action is permanent.
- The action will be recorded in the audit log.
- Current sessions are not affected.

Actions:
[Cancel] [Delete Policy]
```

### Modal rules

- Use specific titles.
- Name the object being changed.
- Explain consequences.
- Use destructive button styling only for destructive confirmation.
- Keep cancel available and visually clear.
- Do not use vague “Are you sure?” copy by itself.

---

## Interaction and Accessibility

### Keyboard support

All interactive controls must be keyboard-accessible:

- sidebar items
- tabs
- buttons
- inputs
- selects
- toggles
- checkboxes
- tables with row actions
- modals
- menus
- pagination

Focus states must be visible and high contrast.

### Focus style

Use a visible focus ring.

Example:

```css
:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}
```

### Contrast

Warmth must not reduce contrast.

Rules:

- Primary text must be strongly readable on all surfaces.
- Helper text should remain readable.
- Disabled text should be visibly disabled but still legible.
- Error text should not rely on red alone.
- Status should include text, not only color.

### Feedback

Every meaningful action should produce feedback:

- save success
- validation error
- background task started
- maintenance task completed
- destructive action confirmed
- permission denied
- network or server failure

Use inline feedback when it belongs to a specific field or section. Use toast-style feedback for page-level actions, but do not rely on toasts for critical failures.

### Loading states

Use calm, local loading states.

Examples:

- button spinner while saving
- table skeleton while loading rows
- disabled submit while request is in flight
- background task status row

Do not blank the entire page for small updates.

---

## Content and Wording

The UI should use direct, audit-friendly language.

### Good button labels

- Save changes
- Create user
- Add rule
- Preview policy
- Run diagnostic
- Rotate keys
- Export audit log
- Disable user

### Avoid

- Submit
- OK
- Proceed
- Execute
- Do it
- Apply stuff
- Manage
- Configure now

### Error messages

Good:

```text
Session timeout must be between 15 minutes and 24 hours.
```

Bad:

```text
Invalid input.
```

### Warnings

Good:

```text
This policy is disabled and will not affect file uploads until enabled.
```

Bad:

```text
Warning!
```

### Empty states

Empty states should suggest the next maintenance action.

Example:

```text
No policies have been created yet.
Create a policy to define what users and services are allowed to do.

[Create policy]
```

---

## Responsive Behavior

### Wide desktop

Use:

- expanded sidebar
- full primary content width
- optional right rail
- tables with all important columns
- persistent save/discard bar for long forms

### Medium screens

Use:

- narrower sidebar or collapsible sidebar
- right rail moves below primary content
- tables may hide lower-priority columns
- filters may wrap into multiple rows

### Small screens

Administration tools are often desktop-first, but small screens should remain usable for urgent maintenance.

Use:

- collapsed navigation drawer
- single-column forms
- stacked metadata panels
- horizontal table scrolling only when necessary
- sticky primary actions
- readable touch targets

Do not compress dense controls below usability.

---

## Example Screen: Policy Editor

This example shows how the Warm Light system comes together.

```text
┌────────────────────────────────────────────────────────────────────┐
│ Search settings, users, policies...                   Help  Admin  │
├──────────────┬─────────────────────────────────────────────────────┤
│ Manage       │ Edit Policy: File Upload Restrictions                │
│  Users       │ Rules are evaluated from top to bottom.              │
│  Roles       │                                      [Save Changes]  │
│  Policies *  │ Summary | Rules * | Exceptions | Auditing            │
│              ├───────────────────────────────────────┬─────────────┤
│ Configure    │ Policy Rules                          │ Status      │
│  Settings    │ ┌────┬──────────────┬────────┬──────┐ │ Active      │
│  Secrets     │ │ #  │ Condition    │ Action │Effect│ │ Enforced    │
│  Hardware    │ ├────┼──────────────┼────────┼──────┤ │ Updated     │
│              │ │ 1  │ .exe files   │ Block  │Deny  │ │ Recent      │
│ Operations   │ │ 2  │ images       │ Allow  │Allow │ │ changes     │
│  Logs        │ │ 3  │ all others   │ Block  │Deny  │ │             │
│  Tasks       │ └────┴──────────────┴────────┴──────┘ │             │
│              │ Rule Details                          │             │
│              │ [File type ▼] [is one of ▼] [.exe ×] │             │
│              │ Effect: ( ) Allow  (o) Deny           │             │
├──────────────┴───────────────────────────────────────┴─────────────┤
│ Unsaved changes                                  [Discard] [Save]  │
└────────────────────────────────────────────────────────────────────┘
```

### Why this works

- The sidebar establishes location.
- The page title states the task.
- The tab row breaks a complex policy into understandable sections.
- The table preserves dense rule information.
- The editor below the table supports focused changes.
- The right rail provides status and audit context.
- The sticky save bar makes the final action predictable.

---

## Implementation Notes

This specification is intentionally vendor- and framework-neutral.

It can be implemented with:

- server-rendered HTML
- client-rendered components
- web components
- native desktop webviews
- any CSS methodology
- any design system tooling

The important implementation requirements are:

- consistent layout primitives
- reusable form components
- reusable table components
- accessible controls
- clear semantic states
- predictable save/discard behavior
- strong audit and confirmation patterns

---

## Checklist

Use this checklist when reviewing a Warm Light admin screen.

### Layout

- [ ] Persistent sidebar is present or intentionally collapsed.
- [ ] Page title clearly states the current task.
- [ ] Primary action is easy to find.
- [ ] Main content is visually dominant.
- [ ] Supporting context is secondary.
- [ ] Destructive actions are separated.

### Forms

- [ ] Labels are visible and close to fields.
- [ ] Helper text explains non-obvious settings.
- [ ] Errors appear close to fields.
- [ ] Save/discard behavior is consistent.
- [ ] High-risk settings explain impact.
- [ ] Inputs have visible focus states.

### Tables

- [ ] Column labels are clear.
- [ ] Row density is readable.
- [ ] Filters/search appear above the table.
- [ ] Pagination appears below the table.
- [ ] Status is text plus color/icon.
- [ ] Row actions are consistent.

### Visual design

- [ ] Warm neutrals define the base.
- [ ] Amber is used sparingly.
- [ ] Contrast remains strong.
- [ ] Borders and shadows are subtle.
- [ ] Typography hierarchy is clear.
- [ ] Dense areas remain scannable.

### Accessibility

- [ ] All controls are keyboard accessible.
- [ ] Focus states are visible.
- [ ] Color is not the only state indicator.
- [ ] Text contrast is sufficient.
- [ ] Modals trap focus correctly.
- [ ] Error messages are specific.

### Maintenance safety

- [ ] Dangerous actions require confirmation.
- [ ] Consequences are explained.
- [ ] Audit-relevant language is clear.
- [ ] Irreversible actions are visually separated.
- [ ] Background tasks show progress or status.
- [ ] Permission failures are clearly reported.
