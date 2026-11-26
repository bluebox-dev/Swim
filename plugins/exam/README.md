# Exam mode (plugin)

Limits what a machine can do during an exam and keeps evidence of violations.

```bash
builder-infra/scripts/plugin.sh enable exam
```

Then open **โหมดสอบ / Exam mode** in the console menu.

## What can be set

| Area | Settings |
|---|---|
| Web | only the listed sites · every site except a block list (with a built-in list of AI sites) · no limit |
| Browser | extensions off, private windows off, developer tools off (Edge, Chrome, Brave, Firefox) |
| Programs | built-in groups (AI, chat, remote control) and your own list; closed when they start |
| Machine | Windows Copilot and the Edge sidebar off, USB storage off |
| Evidence | screenshot on a violation, watched words in window titles, timed screenshots |

Set-ups can be saved by name and applied to a whole room or to selected machines.

## Evidence

Each finding has the screenshot, the machine's time, the time the server received it, the
signed-in user, the program or window that caused it, and how long the machine had been
idle. Only a program **with a window the person could see** counts as a violation; a
background process is closed and recorded separately, without a screenshot. The server
stores the SHA-256 of each screenshot on arrival and refuses to show a file that changed
afterwards.

An administrator can delete evidence at any time (one item, or everything matching a
filter); every deletion is in the activity log. What is kept is deleted automatically
after `SWIM_EXAM_KEEP_DAYS` (default 30). The person at the machine is told that it is
watched when exam mode starts.

## Limits

- It does not see phones or other devices.
- Browsers other than Edge, Chrome, Brave and Firefox are not web-limited: put them on the
  program list, and use the install lock so users cannot install another one.
- Edge and Chrome read new settings on a Group Policy refresh, which the agent triggers;
  a browser that was open may still take a few seconds.
- A screenshot of a locked or sleeping screen shows the last picture; the idle time beside
  it says so.

## Where things are

| | |
|---|---|
| Tables | `exam_profiles`, `exam_hosts`, `exam_events` |
| Screenshots | `<SWIM_DATA>/plugins/exam/` on the server, not served by the web server |
| API | `/v1/exam/*`, `POST /v1/agent/exam/events` |
| On a machine | `%ProgramData%\SWIM\exam\`, policy keys marked `SwimExam=1` |
| Diagnostic tool | `run_script` with `{"tool": "exam-check"}` (read-only) |
