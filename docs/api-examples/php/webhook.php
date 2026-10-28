<?php
// Приёмник вебхуков Vynel: положите файл на сервер и укажите его адрес в панели.
// Секрет — в переменной окружения веб-сервера: VYNEL_WEBHOOK_SECRET=whsec_...
//
// Для проверки без веб-сервера: VYNEL_WEBHOOK_SECRET=whsec_... php -S 127.0.0.1:8000 webhook.php

$secret = getenv('VYNEL_WEBHOOK_SECRET');
$raw = file_get_contents('php://input');          // подпись считается по сырому телу
$ts = $_SERVER['HTTP_X_VYNEL_TIMESTAMP'] ?? '';
$sig = $_SERVER['HTTP_X_VYNEL_SIGNATURE'] ?? '';

$ok = ctype_digit($ts) && abs(time() - (int)$ts) <= 300       // не повтор перехваченного запроса
    && hash_equals('sha256=' . hash_hmac('sha256', $ts . '.' . $raw, $secret), $sig);
if ($_SERVER['REQUEST_METHOD'] !== 'POST' || !$ok) {
    http_response_code(401);
    exit;
}

$event = json_decode($raw, true);
if ($event['type'] !== 'ping') {
    // Событие может прийти дважды: ведите таблицу обработанных $event['id'] и пропускайте повторы.
    error_log(sprintf('%s %s %s', $event['type'], $event['entity']['username'] ?? $event['entity']['code'] ?? '', json_encode($event['data'])));
}
http_response_code(204); // быстро и 2xx, иначе панель повторит позже
