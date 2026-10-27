# Sending alerts from scripts (API)

Anything that can make an HTTPS request can send a Foghorn alert: a PowerShell
script, a monitoring system, a fire-panel relay with a Raspberry Pi behind it.

## 1. Create a token

Web console → **Settings** → **API tokens** → **New token**. Name it after what
will use it. Copy the token (`fh_…`) when it is shown — it is not shown again.

Choose what the token may do:

| Scope | Allows |
|---|---|
| `send` | Send, recall and schedule alerts; save templates. |
| `lock` | Lock and unlock screens. |
| `read` | Read computers, groups, sounds, templates, alert and lock history. |

No token can change settings, accounts, groups or webhooks, or upload sounds.
Alerts and locks it makes are signed with the token's name. Delete the token
to cut off whatever was using it. Tokens made before 1.1 keep working with
`send` and `read`.

**Building something bigger?** [INTEGRATIONS-LARAVEL.md](INTEGRATIONS-LARAVEL.md)
has a ready-made client for Laravel. The full machine-readable description is
at `GET /api/openapi.json` (no token needed). Import it into Postman or
Insomnia, or generate a client from it. The console's **API** page shows the
same reference.

Send it on every request as a header:

```
Authorization: Bearer fh_your_token_here
```

## 2. Send an alert

`POST /api/alerts` with a JSON body.

**PowerShell**

```powershell
$token = 'fh_your_token_here'
$alert = @{
    title   = 'Server maintenance at 17:00'
    message = 'Shared drives will be unavailable for about 20 minutes. Save your work before then.'
    level   = 'warning'
    display = 'corner'
    target  = @{ all = $true }
} | ConvertTo-Json -Depth 5

Invoke-RestMethod -Method Post -Uri 'http://foghorn01:8080/api/alerts' `
    -Headers @{ Authorization = "Bearer $token" } `
    -ContentType 'application/json; charset=utf-8' `
    -Body ([Text.Encoding]::UTF8.GetBytes($alert))
```

**curl**

```bash
curl -X POST http://foghorn01:8080/api/alerts \
  -H "Authorization: Bearer fh_your_token_here" \
  -H "Content-Type: application/json" \
  -d '{"title":"Leave the building now","level":"critical","display":"fullscreen","require_ack":true,"sound":true,"target":{"all":true}}'
```

The reply (HTTP `201`) includes the alert's `id` and `online_reach`, the number
of computers that were on and matched at that moment:

```json
{ "alert": { "id": "9f2c41d07a3be815", "status": "active", "...": "..." }, "online_reach": 212 }
```

### Fields

| Field | Type | Default | Meaning |
|---|---|---|---|
| `title` | text, ≤120 | **required** | The headline. |
| `message` | text, ≤2000 | empty | Body text. Line breaks (`\n`) are kept. Plain text only. |
| `level` | `info` \| `warning` \| `critical` | `info` | Shown as Notice / Warning / Urgent. |
| `display` | `corner` \| `center` \| `fullscreen` | `corner` | Where it appears. |
| `require_ack` | true/false | false | Must be closed with "I've read this"; acknowledgements are recorded. Forces `display_seconds` to 0. |
| `display_seconds` | 0–86400 | 0 | Close itself after this long. 0 = stays until closed. |
| `sound` | true/false | false | Play the Windows alert sound. |
| `link` | `http(s)://…`, ≤500 | empty | Adds an "Open link" button. |
| `expires_minutes` | 1–10080 | 10 | Keep delivering to PCs that log on within this many minutes. |
| `target` | object | **required** | Who gets it — see below. |
| `accent` | `#rrggbb` | level colour | Colour of the band (and the full-screen background, darkened). |
| `label` | text, ≤24 | Notice / Warning / Urgent | Replaces the word in the band, e.g. `IT team`. |
| `icon` | name | none | One of `info` `warning` `alarm` `fire` `lock` `bell` `calendar` `wrench` `megaphone` `clock` `check` `heart` `star` `weather` `wifi` `eyes`. |
| `dismiss_label` | text, ≤30 | Dismiss / I've read this | Wording of the close button. |
| `responses` | up to 4 texts, ≤30 each | none | **Reply buttons.** People must pick one; each choice is recorded, totalled in `totals.responses`, and sent to webhooks as `alert.responded`. Forces `require_ack`. |
| `sound_id` | id | none | An uploaded sound (`GET /api/sounds`). Implies `sound: true`. |
| `sound_loop` | true/false | false | Keep playing until the alert is closed or answered. |

### Retrying safely: `Idempotency-Key`

Add an `Idempotency-Key` header (any text up to 200 characters, such as an
order id or a job id). If the same token sends the same key again within 24
hours, Foghorn sends nothing new. It answers HTTP `200` with the alert it
already sent and `"replayed": true`. Use this from anything that retries:
queue workers, webhooks or flaky networks.

```bash
curl -X POST http://foghorn01:8080/api/alerts -H "Authorization: Bearer fh_…" \
  -H "Idempotency-Key: cover-4211" -H "Content-Type: application/json" \
  -d '{"title":"Cover: P3 in S12","target":{"rules":[{"field":"user","pattern":"jsmith"}]},"require_ack":true}'
```

### Asking a question

```json
{ "title": "Fire drill: are you out?", "level": "critical", "display": "fullscreen", "icon": "alarm",
  "responses": ["I'm out safely", "I need help"], "target": { "all": true } }
```

`GET /api/alerts/{id}` then shows `totals.responses`
(`{"I'm out safely": 212, "I need help": 3}`) and a `response` on each
delivery, so you can see who needs help and where they are.

### Target

Exactly one of these shapes:

```json
{ "all": true }
```
```json
{ "group_ids": ["4be19c02d7a35f60"] }
```
```json
{ "rules": [ { "field": "hostname", "pattern": "LAB1-*" },
             { "field": "group",    "pattern": "Students-Year1" } ] }
```

Rules are OR'd: a computer matching **any** rule gets the alert.

| `field` | Matches against | Pattern |
|---|---|---|
| `hostname` | Computer name | Wildcards `*` and `?`. Not case-sensitive. |
| `user` | Logged-on username, with or without `DOMAIN\` | Wildcards. |
| `ou` | The computer's distinguished name | Plain text matches anywhere in it (`OU=Library`); or wildcards against the whole DN. |
| `group` | AD groups in the user's log-on token, with or without `DOMAIN\` | Wildcards. |
| `ip` | The address the PC connects from | CIDR (`10.20.30.0/24`) or wildcards (`10.20.*`). |

Group ids come from `GET /api/groups`.

## 3. Other calls

| Call | Does |
|---|---|
| `GET /api/alerts?limit=50` | Recent alerts, newest first, with totals. |
| `GET /api/alerts/{id}` | One alert with a per-computer delivery list. |
| `POST /api/alerts/{id}/recall` | Take it off every screen and stop delivering it. (A token may recall only alerts it sent.) |
| `POST /api/reach` with a `target` object as the body | How many computers that target matches: `{"online": 23, "known": 41, "sample": ["LAB1-PC01", …]}`. Sends nothing. |
| `GET /api/clients` | Every computer that has checked in, with `online` true/false. |
| `GET /api/groups` | Saved groups with their ids and rules. |
| `GET /api/sounds` | Uploaded sounds, for `sound_id`. `GET /api/sounds/{id}/file` is the audio. |
| `GET /api/templates` | Saved alert designs. |
| `GET /api/meta` | The icons, scopes, webhook events and limits this server accepts. |
| `GET /api/me` | Who the token is and its scopes. A cheap "does my token work?" check. |
| `GET /api/openapi.json` | The full API description. No token needed. |
| `GET /healthz` | `{"status":"ok"}`. No token needed. For monitoring. |

## 4. Lock screens

Needs the `lock` scope. People signed in to the console can lock screens if
they are administrators, or if an administrator has ticked *Can lock screens*
on their account. Either way, a lock only reaches the computers they may send to.

`POST /api/locks`

```json
{ "mode": "blank", "message": "Eyes to the front, please.", "duration_minutes": 15,
  "target": { "rules": [{ "field": "hostname", "pattern": "LAB1-*" }] } }
```

| Field | Values | Default | Meaning |
|---|---|---|---|
| `mode` | `blank` \| `freeze` | `blank` | `blank` covers every monitor with the message. `freeze` leaves the screen visible but blocks keyboard and mouse, with a small banner. |
| `message` | text, ≤500 | empty | Shown on the locked screen. |
| `duration_minutes` | 1–480 | 60 | The lock lifts itself after this long. The PC enforces it too, so a room is never left locked because the server went away. |
| `target` | object | **required** | As for alerts. |

The reply (`201`) is `{"lock": {...}, "online_reach": 23}`. Then:

| Call | Does |
|---|---|
| `POST /api/locks/{id}/unlock` | Unlock. A token may unlock only locks it made; an administrator can unlock any. |
| `POST /api/locks/unlock-all` | Unlock everything the caller may unlock: `{"unlocked": 2}`. |
| `GET /api/locks?active=true` | Locks in force now (`read` scope). Each has `locked`, the number of computers showing it. |
| `GET /api/locks/{id}` | One lock, with each computer's `locked_at` and `released_at`. |

Alerts still show above a locked screen and can be clicked, so an emergency
alert reaches a locked room. What a lock can and cannot stop is described in
[SECURITY.md](SECURITY.md#screen-locks).

## 5. Schedule alerts

Needs the `send` scope. Times are the Foghorn server's local time. A schedule
sends as whoever made it, with that account's restrictions.

```json
POST /api/schedules
{ "name": "Home time", "days": [1, 2, 3, 4, 5], "time": "15:50",
  "spec": { "title": "Save your work", "message": "PCs shut down at 16:00.", "target": { "all": true } } }
```

Or once: `"once": "2026-11-03T09:00"` instead of `days` and `time`. `days`
run from `0` (Sunday) to `6` (Saturday).

| Call | Does |
|---|---|
| `GET /api/schedules` | All schedules with `next_run`, `last_run`, `last_alert` and `last_error`. |
| `PUT /api/schedules/{id}` | Change one (same body; add `"enabled": false` to pause it). |
| `POST /api/schedules/{id}/run` | Send it now, without changing its timetable. |
| `DELETE /api/schedules/{id}` | Delete it. |

If the server was off when a run was due, that run is skipped (and noted in
`last_error`) rather than sent late.

## 6. Sounds

Uploading is for administrators signed in to the console (or with their
session). The body is the raw file, WAV or MP3, up to 5 MB:

```bash
curl -X POST "http://foghorn01:8080/api/sounds?name=Fire%20bell" \
  -H "X-Foghorn: 1" -b "foghorn_session=…" -H "Content-Type: audio/wav" --data-binary @firebell.wav
```

Each sound's `id` comes from its content, so PCs download a sound once and
keep it. Keep sounds short: the first time one is used, every PC that shows
the alert downloads it.

## 7. Webhooks: hear about things as they happen

An administrator adds webhooks under **Settings → Webhooks**: an address, the
events wanted, and a signing secret Foghorn generates. Foghorn then POSTs
JSON to that address:

```json
{ "id": "4be19c02d7a35f60", "event": "alert.responded", "created_at": "2026-10-28T09:40:00Z",
  "org": "Northbrook College",
  "data": { "alert_id": "9f2c41d07a3be815", "title": "Fire drill: are you out?",
            "hostname": "LAB1-PC07", "user": "jsmith", "domain": "COLLEGE",
            "response": "I need help", "at": "2026-10-28T09:40:00Z" } }
```

| Event | When |
|---|---|
| `alert.sent` | An alert was sent (by anyone, or a schedule). |
| `alert.acknowledged` | Someone pressed "I've read this". |
| `alert.responded` | Someone pressed a reply button; `data.response` says which. |
| `alert.recalled` | An alert was recalled. |
| `lock.started` / `lock.unlocked` | Screens were locked or unlocked. |
| `ping` | The **Test** button in Settings. |

**Check the signature.** Each delivery carries `X-Foghorn-Timestamp` and
`X-Foghorn-Signature: sha256=<hex>`, where `<hex>` is the HMAC-SHA256 of
`"<timestamp>.<raw body>"` keyed with the secret. Compare in constant time,
and refuse timestamps more than a few minutes old.

```php
$expected = 'sha256='.hash_hmac('sha256', $timestamp.'.'.$rawBody, $secret);
if (!hash_equals($expected, $signature)) { http_response_code(401); exit; }
```

```python
expected = "sha256=" + hmac.new(secret.encode(), f"{ts}.".encode() + raw_body, hashlib.sha256).hexdigest()
if not hmac.compare_digest(expected, signature): abort(401)
```

Answer with any `2xx` within 10 seconds. A failed delivery is retried after
5 s, 30 s and 2 min, then dropped; the last error shows in Settings. A retry
has the same `id`, so ignore ids you have already handled.

## Errors

Errors are JSON with a code and a sentence you can show to a person:

```json
{ "error": "invalid", "message": "The link must be a full http:// or https:// address." }
```

| HTTP | `error` | Meaning |
|---|---|---|
| 400 | `invalid`, `bad_json` | Something wrong with the request; `message` says what. |
| 401 | `not_signed_in` | Token missing, mistyped or deleted. |
| 403 | `forbidden` | The token (or account) is not allowed to do that. |
| 404 | `not_found` | No such alert / endpoint. |

## Example: recall after ten minutes

```powershell
$h = @{ Authorization = "Bearer $token" }
$r = Invoke-RestMethod -Method Post -Uri "$server/api/alerts" -Headers $h -ContentType 'application/json' -Body $alert
Start-Sleep -Seconds 600
Invoke-RestMethod -Method Post -Uri "$server/api/alerts/$($r.alert.id)/recall" -Headers $h
```

> Treat a token like a password. Keep it out of scripts that students can read;
> on Windows, store it with `Export-Clixml` under the service account that runs
> the script, or in your secrets manager.
