<?php

namespace App\Services;

use Illuminate\Http\Client\PendingRequest;
use Illuminate\Http\Client\RequestException;
use Illuminate\Support\Facades\Http;
use RuntimeException;

/**
 * A small client for the Foghorn API.
 *
 *   app(Foghorn::class)->send('Parents\' evening tonight', target: Foghorn::everyone());
 *
 * Configure it in config/services.php (see docs/INTEGRATIONS-LARAVEL.md).
 * Every method throws FoghornException with Foghorn's own explanation if the
 * server says no, so you can show it to a person or log it as it is.
 */
class Foghorn
{
    public function __construct(
        private string $url,
        private string $token,
        private int $timeout = 10,
    ) {}

    public static function fromConfig(): self
    {
        $c = config('services.foghorn');
        if (empty($c['url']) || empty($c['token'])) {
            throw new RuntimeException('Set FOGHORN_URL and FOGHORN_TOKEN in .env.');
        }
        return new self(rtrim($c['url'], '/'), $c['token'], (int) ($c['timeout'] ?? 10));
    }

    // ---- targets ----------------------------------------------------------

    /** Every computer. */
    public static function everyone(): array
    {
        return ['all' => true];
    }

    /** Saved groups (rooms, departments) by id - see groups(). */
    public static function groups(string ...$ids): array
    {
        return ['group_ids' => $ids];
    }

    /** Computers whose name matches, e.g. Foghorn::computers('LAB1-*', 'LIB-PC07'). */
    public static function computers(string ...$patterns): array
    {
        return ['rules' => array_map(fn ($p) => ['field' => 'hostname', 'pattern' => $p], $patterns)];
    }

    /** Whoever is logged on as these Windows usernames, e.g. Foghorn::users('jsmith'). */
    public static function users(string ...$patterns): array
    {
        return ['rules' => array_map(fn ($p) => ['field' => 'user', 'pattern' => $p], $patterns)];
    }

    /** Members of AD groups, e.g. Foghorn::adGroups('Staff-Science'). */
    public static function adGroups(string ...$patterns): array
    {
        return ['rules' => array_map(fn ($p) => ['field' => 'group', 'pattern' => $p], $patterns)];
    }

    // ---- alerts -----------------------------------------------------------

    /**
     * Sends an alert and returns it (with its 'id').
     *
     * $options takes any field from the API: message, level (info|warning|critical),
     * display (corner|center|fullscreen), require_ack, display_seconds, link,
     * expires_minutes, accent ('#c3301c'), label, icon, dismiss_label,
     * responses (['Yes', 'No']), sound, sound_id, sound_loop.
     *
     * Pass $idempotencyKey (an order id, an event id...) when this runs in a
     * queued job: if the job is retried, Foghorn returns the alert it already
     * sent instead of sending a second one.
     */
    public function send(string $title, array $target, array $options = [], ?string $idempotencyKey = null): array
    {
        $request = $this->http();
        if ($idempotencyKey !== null) {
            $request = $request->withHeaders(['Idempotency-Key' => $idempotencyKey]);
        }
        $body = ['title' => $title, 'target' => $target] + $options;
        return $this->call(fn () => $request->post('/api/alerts', $body))['alert'];
    }

    /** One alert with who has seen it, confirmed it, and what they replied. */
    public function alert(string $id): array
    {
        return $this->call(fn () => $this->http()->get("/api/alerts/{$id}"));
    }

    /** Takes an alert off every screen. */
    public function recall(string $id): array
    {
        return $this->call(fn () => $this->http()->post("/api/alerts/{$id}/recall"));
    }

    /** How many computers a target reaches right now, without sending anything. */
    public function reach(array $target): array
    {
        return $this->call(fn () => $this->http()->post('/api/reach', $target));
    }

    // ---- screen locks (the token needs the "lock" scope) -------------------

    /** Locks screens. $mode is 'blank' (cover the screen) or 'freeze' (screen visible, input blocked). */
    public function lock(array $target, string $message = '', int $minutes = 15, string $mode = 'blank'): array
    {
        return $this->call(fn () => $this->http()->post('/api/locks', [
            'target' => $target, 'message' => $message, 'duration_minutes' => $minutes, 'mode' => $mode,
        ]))['lock'];
    }

    public function unlock(string $lockId): array
    {
        return $this->call(fn () => $this->http()->post("/api/locks/{$lockId}/unlock"));
    }

    /** Lifts every lock this token made. */
    public function unlockAll(): int
    {
        return $this->call(fn () => $this->http()->post('/api/locks/unlock-all'))['unlocked'];
    }

    // ---- scheduling ---------------------------------------------------------

    /** Sends at a set time once, e.g. scheduleOnce(now()->addDay()->setTime(9, 0), ...). */
    public function scheduleOnce(\DateTimeInterface $at, string $title, array $target, array $options = [], ?string $name = null): array
    {
        return $this->call(fn () => $this->http()->post('/api/schedules', [
            'name' => $name ?? $title,
            'once' => $at->format('Y-m-d\TH:i'), // Foghorn's server-local time
            'spec' => ['title' => $title, 'target' => $target] + $options,
        ]));
    }

    /** Sends every week. $days: 0 = Sunday ... 6 = Saturday; $time '15:50'. */
    public function scheduleWeekly(array $days, string $time, string $title, array $target, array $options = [], ?string $name = null): array
    {
        return $this->call(fn () => $this->http()->post('/api/schedules', [
            'name' => $name ?? $title, 'days' => $days, 'time' => $time,
            'spec' => ['title' => $title, 'target' => $target] + $options,
        ]));
    }

    // ---- lookups ------------------------------------------------------------

    /** Saved groups, with their ids and how many computers are on. */
    public function listGroups(): array
    {
        return $this->call(fn () => $this->http()->get('/api/groups'));
    }

    /** Every computer that has checked in, with 'online' true/false. */
    public function computersOnline(): array
    {
        return array_values(array_filter(
            $this->call(fn () => $this->http()->get('/api/clients')),
            fn ($c) => $c['online'],
        ));
    }

    /** Uploaded sounds; use an 'id' as an alert's sound_id. */
    public function sounds(): array
    {
        return $this->call(fn () => $this->http()->get('/api/sounds'));
    }

    // ---- plumbing -------------------------------------------------------------

    private function http(): PendingRequest
    {
        return Http::baseUrl($this->url)
            ->withToken($this->token)
            ->acceptJson()
            ->asJson()
            ->timeout($this->timeout)
            // Connection hiccups are retried; Foghorn's own "no" (4xx) is not.
            ->retry(3, 500, fn ($e) => ! $e instanceof RequestException, throw: false);
    }

    private function call(callable $make): array
    {
        $response = $make();
        if ($response->failed()) {
            throw new FoghornException(
                $response->json('message') ?? "Foghorn answered HTTP {$response->status()}.",
                $response->json('error') ?? 'http_'.$response->status(),
                $response->status(),
            );
        }
        return $response->json() ?? [];
    }
}
