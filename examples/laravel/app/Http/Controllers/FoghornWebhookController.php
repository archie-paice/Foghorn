<?php

namespace App\Http\Controllers;

use App\Events\FoghornEventReceived;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\Cache;
use Illuminate\Support\Facades\Log;

/**
 * Receives Foghorn webhooks: alert.sent, alert.acknowledged, alert.responded,
 * alert.recalled, lock.started, lock.unlocked (and "ping" from the Test button).
 *
 * It checks the signature, ignores stale or repeated deliveries, then fires a
 * normal Laravel event - put your own logic in a listener for
 * FoghornEventReceived, not here.
 */
class FoghornWebhookController extends Controller
{
    /** Deliveries older than this are refused, so a captured one cannot be replayed later. */
    private const TOLERANCE_SECONDS = 300;

    public function __invoke(Request $request)
    {
        $secret = (string) config('services.foghorn.webhook_secret');
        $timestamp = (string) $request->header('X-Foghorn-Timestamp');
        $signature = (string) $request->header('X-Foghorn-Signature');
        $body = $request->getContent();

        $expected = 'sha256='.hash_hmac('sha256', $timestamp.'.'.$body, $secret);
        if ($secret === '' || ! hash_equals($expected, $signature)) {
            Log::warning('Foghorn webhook with a bad signature', ['ip' => $request->ip()]);
            abort(401, 'Bad signature');
        }
        if (! ctype_digit($timestamp) || abs(time() - (int) $timestamp) > self::TOLERANCE_SECONDS) {
            abort(401, 'Stale delivery');
        }

        $payload = $request->json()->all();

        // Foghorn retries until it gets a 2xx, so the same event can arrive
        // twice. Its id is the same on every attempt.
        if (! Cache::add('foghorn-webhook:'.$payload['id'], true, now()->addDay())) {
            return response()->noContent();
        }

        event(new FoghornEventReceived($payload['event'], $payload['data'] ?? [], $payload));

        return response()->noContent();
    }
}
