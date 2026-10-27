<?php

namespace App\Services;

use RuntimeException;

/** Foghorn refused a request. The message is Foghorn's own explanation, fit to show a person. */
class FoghornException extends RuntimeException
{
    public function __construct(string $message, public readonly string $errorCode, public readonly int $status)
    {
        parent::__construct($message, $status);
    }
}
