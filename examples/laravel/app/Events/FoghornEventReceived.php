<?php

namespace App\Events;

use Illuminate\Foundation\Events\Dispatchable;

/**
 * Something happened in Foghorn. $name is the event, e.g. "alert.responded";
 * $data is its details - for alert.responded and alert.acknowledged:
 *
 *   ['alert_id' => '...', 'title' => '...', 'hostname' => 'LAB1-PC07',
 *    'user' => 'jsmith', 'domain' => 'COLLEGE', 'response' => 'I need help', 'at' => '...']
 */
class FoghornEventReceived
{
    use Dispatchable;

    public function __construct(
        public string $name,
        public array $data,
        public array $payload,
    ) {}
}
