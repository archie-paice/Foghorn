# Using Foghorn from Laravel

This guide connects a Laravel site (a school website, an intranet, a booking
system) to Foghorn, so it can:

- **send desktop alerts**: "Your cover lesson starts in 10 minutes", "Parents'
  evening tonight", "Server maintenance at 17:00";
- **ask questions and hear the answers**: a fire-drill roll call, "Can you cover
  period 3?";
- **lock screens** for a room, from a timetable or a button in your own app;
- **react when something happens** in Foghorn, through webhooks.

The code is ready to copy from [`examples/laravel/`](../examples/laravel/). It
uses only Laravel's own HTTP client and needs no extra Composer packages.
It works on Laravel 10, 11 and 12 with PHP 8.1 or newer.

| File | What it is |
|---|---|
| `app/Services/Foghorn.php` | The API client: `send`, `recall`, `lock`, `unlock`, `scheduleWeekly`, … |
| `app/Http/Controllers/FoghornWebhookController.php` | Receives webhooks and checks their signatures |
| `app/Events/FoghornEventReceived.php` | The Laravel event the controller fires |
| `app/Console/Commands/FoghornSend.php` | `php artisan foghorn:send` |
| `app/Notifications/Channels/FoghornChannel.php` | Send any Laravel notification as a desktop alert |

---

## 1. Make a token

In the Foghorn console go to **Settings → API tokens → New token**. Name it
after the site (for example "School website") and tick only what it needs:

| Scope | Lets it |
|---|---|
| `send` | send, recall and schedule alerts |
| `read` | see computers, groups, sounds and alert results |
| `lock` | lock and unlock screens |

A site that only sends notices needs `send` and `read`. Copy the token
(`fh_…`); it is shown once.

> The Laravel server has to be able to reach Foghorn. Foghorn normally lives on
> the school network, so a site hosted outside it needs a route in: a VPN, or a
> reverse proxy that exposes **only** `/api/` with HTTPS. Never forward
> Foghorn's port to the open internet.

## 2. Configure

`.env`:

```dotenv
FOGHORN_URL=https://foghorn.school.internal:8443
FOGHORN_TOKEN=fh_paste_your_token_here
FOGHORN_WEBHOOK_SECRET=        # filled in at step 5
```

`config/services.php`:

```php
'foghorn' => [
    'url' => env('FOGHORN_URL'),
    'token' => env('FOGHORN_TOKEN'),
    'webhook_secret' => env('FOGHORN_WEBHOOK_SECRET'),
    'timeout' => 10,
],
```

Copy `examples/laravel/app/Services/Foghorn.php` into `app/Services/`, then
register it in `AppServiceProvider::register()`:

```php
use App\Services\Foghorn;

$this->app->singleton(Foghorn::class, fn () => Foghorn::fromConfig());
```

Check it works:

```bash
php artisan tinker
>>> app(App\Services\Foghorn::class)->listGroups()
```

You should see your rooms and their ids. Copy the ids you will use into
config, for example `'staff_group' => env('FOGHORN_STAFF_GROUP')`.

## 3. Send alerts

```php
use App\Services\Foghorn;

$foghorn = app(Foghorn::class);

// To everyone
$foghorn->send('Parents\' evening tonight', Foghorn::everyone(), [
    'message' => 'Rooms open from 17:00. Staff car park closes at 16:30.',
    'icon' => 'calendar',
]);

// To one room, more urgently, in the middle of the screen
$foghorn->send('Lab 1 is closed this afternoon', Foghorn::computers('LAB1-*'), [
    'level' => 'warning',
    'display' => 'center',
]);

// To one member of staff, wherever they are logged on
$foghorn->send('Your cover lesson starts in 10 minutes', Foghorn::users($teacher->windows_username), [
    'message' => "Room S12, Year 9 Science. Work is on the shared drive.",
    'require_ack' => true,
]);
```

Targets: `Foghorn::everyone()`, `Foghorn::groups($id, …)` (saved groups in
the console), `Foghorn::computers('LAB1-*', …)`, `Foghorn::users('jsmith')`, and
`Foghorn::adGroups('Staff-Science')`.

The builder options are all available as fields: `accent` (`'#6a3fb5'`),
`label`, `icon`, `dismiss_label`, `responses`, `sound_id`, `sound_loop`,
`display_seconds`, `link`. [API.md](API.md#fields) lists every field.

### From a queued job: make it idempotent

Queued jobs can run twice: a worker times out after the request reached
Foghorn but before the job finished. Pass a key that identifies *this*
message, and a retry returns the alert that was already sent instead of
sending a second one:

```php
class NotifyCover implements ShouldQueue
{
    use Queueable;

    public function __construct(public Cover $cover) {}

    public function handle(Foghorn $foghorn): void
    {
        $foghorn->send(
            "Cover: {$this->cover->period} in {$this->cover->room}",
            Foghorn::users($this->cover->teacher->windows_username),
            ['require_ack' => true],
            idempotencyKey: "cover-{$this->cover->id}",
        );
    }
}
```

Foghorn remembers each key for 24 hours, separately for each token.

### As a Laravel notification

Copy `FoghornChannel.php` into `app/Notifications/Channels/`, then:

```php
class CoverAssigned extends Notification implements ShouldQueue
{
    use Queueable;

    public function __construct(public Cover $cover) {}

    public function via($notifiable): array
    {
        return ['mail', FoghornChannel::class];
    }

    public function toFoghorn($notifiable): array
    {
        return ['title' => 'You have a cover lesson', 'message' => "{$this->cover->period}, {$this->cover->room}", 'require_ack' => true];
    }
}
```

On the `User` model:

```php
public function routeNotificationForFoghorn(): array
{
    return Foghorn::users($this->windows_username);
}
```

### From the command line or the scheduler

Copy `FoghornSend.php` into `app/Console/Commands/`:

```bash
php artisan foghorn:send "Server maintenance at 17:00" --message="Save your work." --level=warning
php artisan foghorn:send "Fire drill: are you out?" --reply="I'm out" --reply="I need help" --level=critical --display=fullscreen
```

Foghorn has its own scheduler too (**Scheduled** in the console, or
`$foghorn->scheduleWeekly([1,2,3,4,5], '15:50', 'Save your work', Foghorn::everyone())`).
That keeps working when your website is down.

## 4. Ask a question, read the answers

```php
$alert = $foghorn->send('Fire drill: are you out?', Foghorn::everyone(), [
    'level' => 'critical',
    'display' => 'fullscreen',
    'icon' => 'alarm',
    'responses' => ['I\'m out safely', 'I need help'],
]);

// later, or on a dashboard
$result = $foghorn->alert($alert['id']);
$result['totals']['responses'];   // ['I\'m out safely' => 212, 'I need help' => 3]

collect($result['deliveries'])
    ->where('response', 'I need help')
    ->map(fn ($d) => "{$d['user']} at {$d['hostname']}");
```

To be told as answers arrive, without polling, use a webhook.

## 5. Receive webhooks

In the console, go to **Settings → Webhooks → New webhook**. Set:

- **Address**: `https://your-site/api/foghorn/webhook`
- **Events**: for example `alert.responded` and `lock.started`. Leave all
  unticked to receive everything.

Copy the **signing secret** it shows you into `FOGHORN_WEBHOOK_SECRET`.

Copy `FoghornWebhookController.php` and `FoghornEventReceived.php` into your
app and add the route to **`routes/api.php`**, which has no CSRF check:

```php
use App\Http\Controllers\FoghornWebhookController;

Route::post('/foghorn/webhook', FoghornWebhookController::class);
```

(On Laravel 11+, if `routes/api.php` does not exist, run `php artisan install:api`,
or put the route in `routes/web.php` and exclude it from CSRF in
`bootstrap/app.php` with `$middleware->validateCsrfTokens(except: ['foghorn/webhook']);`.)

Press **Test** next to the webhook in the console. It should say *Test delivered*.

Now listen for the event like any other:

```php
// app/Listeners/HandleFoghornEvent.php
class HandleFoghornEvent
{
    public function handle(FoghornEventReceived $e): void
    {
        if ($e->name === 'alert.responded' && $e->data['response'] === 'I need help') {
            Notification::route('mail', 'reception@school.example')
                ->notify(new StudentNeedsHelp($e->data['user'], $e->data['hostname']));
        }
    }
}
```

On Laravel 11+ listeners are discovered automatically. On 10, register it in
`EventServiceProvider`.

### What arrives

```http
POST /api/foghorn/webhook
Content-Type: application/json
X-Foghorn-Event: alert.responded
X-Foghorn-Delivery: 4be19c02d7a35f60
X-Foghorn-Timestamp: 1793122800
X-Foghorn-Signature: sha256=aa8efe37…
```

```json
{
  "id": "4be19c02d7a35f60",
  "event": "alert.responded",
  "created_at": "2026-10-28T09:40:00Z",
  "org": "Northbrook College",
  "data": {
    "alert_id": "9f2c41d07a3be815",
    "title": "Fire drill: are you out?",
    "hostname": "LAB1-PC07",
    "user": "jsmith",
    "domain": "COLLEGE",
    "response": "I need help",
    "at": "2026-10-28T09:40:00Z"
  }
}
```

| Event | `data` |
|---|---|
| `alert.sent` | `alert` (as `GET /api/alerts/{id}`), `online_reach` |
| `alert.acknowledged` | `alert_id`, `title`, `hostname`, `user`, `domain`, `at` (someone pressed "I've read this") |
| `alert.responded` | the same, plus `response` |
| `alert.recalled` | `alert`, `recalled_by` |
| `lock.started` | `lock`, `online_reach` |
| `lock.unlocked` | `lock`, `unlocked_by` |
| `ping` | `message` (the Test button) |

The controller already handles these rules for you:

- **Signature.** It is `sha256=` followed by the hex HMAC-SHA256 of
  `"{timestamp}.{raw body}"` with the secret. Use the raw body: a re-encoded
  JSON body will not match.
- **Age.** It refuses deliveries more than five minutes old, so a captured
  delivery cannot be replayed later.
- **Duplicates.** Foghorn retries a failed delivery after 5 s, 30 s and 2 min,
  so the same `id` can arrive more than once. The controller ignores repeats.
- **Speed.** Answer within 10 seconds. Put slow work in a queued listener.

## 6. Lock screens

The token needs the `lock` scope.

```php
$lock = $foghorn->lock(Foghorn::computers('LAB1-*'), 'Eyes to the front, please.', minutes: 15);
// ...
$foghorn->unlock($lock['id']);
```

`mode: 'freeze'` keeps the screen visible but blocks keyboard and mouse,
which is useful while a teacher talks over a presentation. Every lock ends by
itself after `minutes`, even if your site never calls `unlock`.

## Errors

Every method throws `App\Services\FoghornException` when Foghorn says no.
`getMessage()` is Foghorn's own explanation, written to be shown to a person.
`$e->errorCode` is the machine-readable code and `$e->status` the HTTP status:

```php
try {
    $foghorn->send($title, Foghorn::groups($roomId));
} catch (FoghornException $e) {
    return back()->withErrors(['foghorn' => $e->getMessage()]);
}
```

Connection failures are retried three times before anything is thrown.
Foghorn's own refusals (a bad field, a missing scope) are not retried.
