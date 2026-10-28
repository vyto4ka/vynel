<?php
// Клиент API Vynel на curl (расширение php-curl), без Composer.
//
//   export VYNEL_API=https://panel.example.com/<секретный путь>/api/v1
//   export VYNEL_TOKEN=vyn_...
//   php client.php

final class VynelError extends RuntimeException
{
    public function __construct(public int $status, public string $errorCode, string $message, public ?string $field = null)
    {
        parent::__construct("$status $errorCode: $message");
    }
}

final class Vynel
{
    public function __construct(
        private ?string $base = null,
        private ?string $token = null,
        private int $retries = 4,
    ) {
        $this->base = rtrim($base ?? getenv('VYNEL_API'), '/');
        $this->token = $token ?? getenv('VYNEL_TOKEN');
    }

    /** Всё, что меняет данные, уходит с Idempotency-Key, и повторы несут тот же ключ. */
    public function request(string $method, string $path, ?array $json = null, array $params = [], ?string $key = null): mixed
    {
        $url = $this->base . $path . ($params ? '?' . http_build_query($params) : '');
        $headers = ['Authorization: Bearer ' . $this->token];
        if ($json !== null) $headers[] = 'Content-Type: application/json';
        if ($method !== 'GET') $headers[] = 'Idempotency-Key: ' . ($key ?? bin2hex(random_bytes(16)));
        for ($attempt = 0;; $attempt++) {
            $ch = curl_init($url);
            curl_setopt_array($ch, [
                CURLOPT_CUSTOMREQUEST => $method,
                CURLOPT_RETURNTRANSFER => true,
                CURLOPT_TIMEOUT => 15,
                CURLOPT_HTTPHEADER => $headers,
                CURLOPT_HEADER => true,
            ]);
            if ($json !== null) curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode($json, JSON_UNESCAPED_UNICODE));
            $out = curl_exec($ch);
            $status = curl_getinfo($ch, CURLINFO_RESPONSE_CODE);
            $headSize = curl_getinfo($ch, CURLINFO_HEADER_SIZE);
            curl_close($ch);
            if ($out === false || $status >= 500 || $status === 429) {
                if ($attempt >= $this->retries) {
                    if ($out === false) throw new RuntimeException('нет ответа от панели');
                } else {
                    $retryAfter = ($out !== false && preg_match('/^retry-after:\s*(\d+)/im', substr($out, 0, $headSize), $m)) ? (int)$m[1] : min(2 ** $attempt, 20);
                    sleep($retryAfter);
                    continue;
                }
            }
            $body = substr($out, $headSize);
            $data = $body === '' ? null : json_decode($body, true);
            if ($status >= 400) {
                $e = $data['error'] ?? [];
                throw new VynelError($status, $e['code'] ?? 'unknown', $e['message'] ?? $body, $e['field'] ?? null);
            }
            return $data;
        }
    }

    public function me(): array { return $this->request('GET', '/me'); }
    public function users(array $filters = []): array { return $this->request('GET', '/users', null, $filters); }
    public function findUser(array $by): ?array { return $this->users($by)['items'][0] ?? null; }
    public function createUser(string $username, array $fields = [], ?string $key = null): array
    {
        return $this->request('POST', '/users', ['username' => $username] + $fields, [], $key);
    }
    public function extend(int $id, int $months = 0, int $days = 0, ?string $key = null): array
    {
        return $this->request('POST', "/users/$id/extend", ['months' => $months, 'days' => $days], [], $key);
    }
    public function disable(int $id): array { return $this->request('POST', "/users/$id/disable"); }
    public function enable(int $id): array { return $this->request('POST', "/users/$id/enable"); }
    public function subscription(int $id): array { return $this->request('GET', "/users/$id/subscription"); }
    public function events(string $after = 'now', int $limit = 100): array
    {
        return $this->request('GET', '/events', null, ['after' => $after, 'limit' => $limit]);
    }
}

if (PHP_SAPI === 'cli' && realpath($argv[0]) === __FILE__) {
    $api = new Vynel();
    $me = $api->me();
    echo 'токен: ', $me['token']['name'], ' ', implode(',', $me['token']['scopes']), "\n";
    echo 'пользователей: ', $api->users(['limit' => 1])['total'], "\n";
}
