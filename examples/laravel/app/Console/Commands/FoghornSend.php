<?php

namespace App\Console\Commands;

use App\Services\Foghorn;
use App\Services\FoghornException;
use Illuminate\Console\Command;

/**
 * php artisan foghorn:send "Server maintenance at 17:00" --message="Save your work." --level=warning
 * php artisan foghorn:send "Lab 1 closed" --computers="LAB1-*" --display=center
 * php artisan foghorn:send "Fire drill: are you out?" --reply="I'm out" --reply="I need help" --level=critical --display=fullscreen
 *
 * Also handy from the scheduler in routes/console.php:
 *   Schedule::command('foghorn:send "Save your work" --message="PCs shut down at 16:00"')->weekdays()->at('15:50');
 */
class FoghornSend extends Command
{
    protected $signature = 'foghorn:send
        {title : The headline}
        {--message= : Body text}
        {--level=info : info, warning or critical}
        {--display=corner : corner, center or fullscreen}
        {--computers=* : Computer name patterns, e.g. LAB1-* (default: everyone)}
        {--group=* : Saved group ids}
        {--reply=* : Reply buttons; people must pick one}
        {--icon= : alarm, bell, calendar, fire, lock, megaphone, ...}
        {--ack : Make people confirm they have read it}';

    protected $description = 'Send a Foghorn desktop alert';

    public function handle(Foghorn $foghorn): int
    {
        $target = match (true) {
            (bool) $this->option('group') => Foghorn::groups(...$this->option('group')),
            (bool) $this->option('computers') => Foghorn::computers(...$this->option('computers')),
            default => Foghorn::everyone(),
        };

        $reach = $foghorn->reach($target);
        if (! $this->confirm("Send to {$reach['online']} computer(s) that are on now?", true)) {
            return self::FAILURE;
        }

        try {
            $alert = $foghorn->send($this->argument('title'), $target, array_filter([
                'message' => $this->option('message'),
                'level' => $this->option('level'),
                'display' => $this->option('display'),
                'responses' => $this->option('reply') ?: null,
                'icon' => $this->option('icon'),
                'require_ack' => $this->option('ack') ?: null,
            ], fn ($v) => $v !== null));
        } catch (FoghornException $e) {
            $this->error($e->getMessage());
            return self::FAILURE;
        }

        $this->info("Sent. Alert id {$alert['id']}");
        return self::SUCCESS;
    }
}
