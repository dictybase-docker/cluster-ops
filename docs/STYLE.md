# Documentation Style Guide

Rules for writing and editing documentation in this repository. Extracted from
the refactoring history of the cluster docs (Aug 2026 onward). Follow these
whenever you create or edit any file under `docs/`.

## 1. Guide vs Reference Split

Every topic has **two layers**:

- **Guide** (`docs/<topic>.md`) — the lean, sequential operator runbook. One
  command per step. No deep explanation.
- **Reference docs** (`docs/reference/<area>/<subject>.md`) — one file per
  subject, holding everything the guide omits: behavior walkthroughs, flag
  tables, rationale, warnings, lifecycle notes.

The guide never re-explains what a reference doc or another guide already
covers. It links instead:

```markdown
→ [Backup details](reference/arangodb/backup.md)
```

The reference doc links back on its first lines:

```markdown
# ArangoDB Backup Details

Back to: [ArangoDB Deploy Guide](../../arangodb-deploy.md)
```

## 2. Guide Section Shape

Every numbered section in a guide has exactly three parts, in this order:

1. **One or two summary lines** — what the step creates or does, and the one
   thing the operator must know before running it.
2. **A `→ [X details](...)` link** to the reference doc (use a `#anchor` when
   pointing at a specific section). Every numbered section has one. If no
   reference doc covers the step yet, create it — a step with nowhere to link
   is a missing reference doc, not an excuse for prose in the guide.
3. **A single fenced bash block** with the minimal invocation.

**Nothing follows the bash block.** No trailing paragraph, no flag tour, no
enumeration of what the command prints, no connection strings, no "what you
get" inventory, no notes about the recipe's own output text. The only
exceptions: one bold warning line at the head of a destructive section, and
one skip/ordering line at the head of an optional step.

Rationale, flag tables, failure modes, and "how it works" prose do **not**
belong in the guide. Move them to the reference doc and link.

Reference doc sections use the consistent shape: *What It Does* →
*Behavior* (numbered steps of what the recipe does) → *Flags* (table with
Required / Default / Notes columns) → warnings/lifecycle.

### Trust the recipe

Recipes validate their own preconditions and fail loudly with a named cause.
Do not pre-explain guard flags, permission requirements, or failure modes in
the guide — the operator meets them only if they happen, and the
troubleshooting reference owns that mapping.

### Order by operator intent

Section order is the order the operator runs things, and a step that must
precede another is never documented after it. A step that only takes effect
at creation time (one-shot bootstrap, an `--option` consumed by a later
recipe) belongs *inside* the creation flow as its own numbered step, not as a
follow-up section — structure that contradicts the prose is the single
biggest source of confusion in these guides. When a guide serves more than
one intent, open the Quick Reference with an intent → steps table.

## 3. Commands Default from the Cluster Environment

Guides assume the operator is inside `just cluster-env` and show the
**minimal** invocation. State the env contract once, near the top of the
guide, not per section.

- Never hardcode `--stack prod`; recipes default to `$PULUMI_STACK`.
- Flags that can default from env (`$PROJECT_ID`, key-file paths) are omitted
  from the example; their defaults are documented in the reference doc's flag
  table.
- One composite recipe per operator action. If a step needs several commands,
  fold them into one recipe instead of lengthening the guide.

## 4. Guide Skeleton

Guides open with, in order:

1. **Title + status line** (e.g. "Production procedure. Use Pulumi.<cluster>.yaml…").
2. **Table of Contents** with anchor links — keep it in sync with headings.
3. **Quick Reference** — the full flow as one numbered bash block for
   experienced users, plus a separate optional-steps block. Comments in this
   block are terse (`# 1. …`, `# 2. …`).
4. **Numbered sections** in execution order, ending with Verify,
   Troubleshooting (link only), and Related Documents.

## 5. Links and Anchors

- References to other docs are always markdown links, never plain-text
  filenames. Use deep links with `#anchors` when pointing at a section.
- Renumbering a section means updating **every** inbound link and anchor in
  the same change — search the whole repo for the old anchor.
- Relative paths from `docs/reference/<area>/` to a guide are
  `../../<guide>.md`; guide → reference is `reference/<area>/<file>.md`.

## 6. Risk and Context Boundaries Must Be Visible

Anything the operator can get silently wrong gets structural emphasis, not a
buried sentence:

- **Destructive operations**: bold warning line at the section head
  (`**Destructive. Clone only.**`), plus the specific hazard (e.g.
  `ForceDestroy: true` deletes all snapshots) in the reference doc.
- **Environment/context switches** (e.g. run this in the *source* cluster's
  shell): split the code into separate blocks with a `---` rule and bold
  labels naming which environment each block runs in. Never rely on a comment
  inside one long block.
- **Non-idempotent actions** (e.g. minting service-account keys): call out
  that re-running accumulates state, and give the audit command.
- **Pairs that must not be mixed** (e.g. `dictycr` vs `dictycr-source`):
  state which is which, who uses each, and what guard exists.

## 7. Writing Voice

- Terse, present tense, operator-facing. Fragments are fine.
- Name the failure the reader will hit and its cause
  ("Outside the sub-shell recipes fail with `Error: no stack name`"), not
  abstract advice.
- Em-dash chains and bold for the one critical term per sentence; no emoji,
  no exclamation marks.
- Mark forward-looking or not-yet-implemented paths explicitly
  ("Reserved for a future revision", "doesn't exist yet") so nobody follows
  them.

## 8. Editing Rules

- **Don't duplicate.** If content exists elsewhere, link. When folding a
  section into another doc, delete the original, don't leave both.
- **Don't grow the guide.** New explanation goes to the reference doc; the
  guide gains at most a line and a link.
- **Relocate, never delete.** Every fact removed from a guide must already
  exist in the matching reference doc, or be added there in that doc's shape
  in the same change. Trimming a guide is a move operation.
- **Keep Quick Reference and TOC in sync** with the body after every edit.
- When a recipe's flags or defaults change, update its reference doc's flag
  table in the same change.
- **Recipe output is documentation.** `Next: …` / `Proceed to …` lines printed
  by a recipe are read more often than the guide. Restructuring a guide means
  updating those echoes in the same change, and they point at the **next
  recipe command**, never at a section number — section numbers drift, recipe
  names don't.
- **Trace claims to a source.** A statement about a tool's default behavior
  (storage layout, implicit field value, operator behavior) cites the schema,
  CRD, or code that proves it. If it cannot be traced, verify it live or leave
  it out — hedged prose ("depends on the plugin default") helps nobody.
