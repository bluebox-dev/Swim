# SWIM plugins

A plugin adds an optional feature to SWIM without becoming part of the core. The first one
is [`exam`](exam/README.md) (exam mode).

## What "optional" means here

A plugin is **compiled in, not switched on**. A plugin that is not enabled is not in the
server binary, not in the agent that runs on the machines, and its web files are not served.
The core contains no reference to any particular plugin: only two small, generic contracts.

| Part | Contract | A plugin provides |
|---|---|---|
| Server | [`backend/plugin`](../backend/plugin/plugin.go) | routes, its own tables, web files, command types, hourly and heartbeat hooks |
| Agent | [`agent/plugin`](../agent/plugin/plugin.go) | command handlers, what it reports, a background service, helper modes |
| Console | `SWIM.plugin({...})` in `frontend/app.js` | a menu page, a card on the machine page, bulk buttons, labels, English text |

## Enable, disable, remove

Run on the server:

```bash
builder-infra/scripts/plugin.sh list
builder-infra/scripts/plugin.sh enable exam
builder-infra/scripts/plugin.sh disable exam            # its data stays
builder-infra/scripts/plugin.sh disable exam --purge    # its tables and files are deleted
```

`enable` and `disable` write `plugins/enabled`, regenerate the two import files
(`backend/cmd/server/plugins_gen.go`, `agent/cmd/swim-agent/plugins_gen.go`), rebuild the
server and the agent, and raise the agent's patch version so machines fetch the new agent
by themselves. `disable` refuses while machines still have the plugin switched on: once the
plugin is out of the agent, nothing could undo what it set on them.

A fresh clone has no `plugins/enabled`, so no plugin is linked in.

## Layout of a plugin

```
plugins/<name>/
  plugin.json     name, description, tables to drop on --purge, the "still in use" check
  contract/       types both halves share (optional)
  server/         Go package: calls plugin.Register in init()
    migrations/   <name>_0001_*.sql, applied at start-up
    web/          plugin.js (and plugin.css): the console half
  agent/          Go package: calls plugin.Register in init(); Windows code behind build tags
```

## Writing one

1. **Server half** — register a `plugin.Plugin`. Everything goes through `plugin.Host`: the
   database, the activity log (`Do` writes one entry in the same transaction as your change),
   signed commands (`PlanCommand`), a private data directory. Keep routes under `/v1/<name>/`
   and `/v1/agent/<name>/`. Name migration files `<name>_NNNN_*.sql`.
2. **Agent half** — register an `agent/plugin.Plugin`. Its commands pass the same checks as
   every command (signature, machine, validity window, at-most-once) before your handler
   runs. `Inventory` is reported as `inventory.plugins.<name>`. A `Service` must recover from
   its own failures.
3. **Console half** — `web/plugin.js` calls `SWIM.plugin({ name, title, icon, page, hostCard,
   bulkButtons, actions, commands, en, init })`. `init(ctx)` hands you the console's helpers
   (`api`, `modal`, `toast`, `card`, …). Follow the console's rules: a confirmation dialog
   before every action, Thai interface text with English in `en`.
4. `plugin.json` — list your tables, and give `active_check` (a query that counts machines
   still using the plugin) if your plugin changes anything on machines.

A plugin must not import the core's internal packages; the compiler enforces it. If the
contract lacks something, extend the contract rather than reaching around it.
