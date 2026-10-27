<?php

namespace App\Notifications\Channels;

use App\Services\Foghorn;
use Illuminate\Notifications\Notification;

/**
 * Lets any Laravel notification pop up on a desktop.
 *
 * In the notification:
 *
 *   public function via($notifiable) { return [FoghornChannel::class]; }
 *
 *   public function toFoghorn($notifiable): array
 *   {
 *       return ['title' => 'Your cover lesson starts in 10 minutes', 'message' => 'Room S12, Year 9 Science.'];
 *   }
 *
 * Who receives it comes from the notifiable's routeNotificationForFoghorn(),
 * e.g. on the User model:
 *
 *   public function routeNotificationForFoghorn() { return Foghorn::users($this->windows_username); }
 *
 * or on the fly: Notification::route(FoghornChannel::class, Foghorn::computers('LAB1-*'))->notify(...)
 */
class FoghornChannel
{
    public function __construct(private Foghorn $foghorn) {}

    public function send(object $notifiable, Notification $notification): void
    {
        $target = $notifiable->routeNotificationFor(self::class, $notification)
            ?? $notifiable->routeNotificationFor('foghorn', $notification);
        if (! $target) {
            return;
        }

        $alert = $notification->toFoghorn($notifiable);
        $title = $alert['title'];
        unset($alert['title']);

        // The notification id is stable across queue retries, so a retried
        // notification never pops up twice.
        $this->foghorn->send($title, $target, $alert, idempotencyKey: 'notification-'.$notification->id);
    }
}
