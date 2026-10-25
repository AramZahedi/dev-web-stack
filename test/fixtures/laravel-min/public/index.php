<?php
declare(strict_types=1);

$uri = parse_url($_SERVER['REQUEST_URI'] ?? '/', PHP_URL_PATH) ?: '/';
$method = $_SERVER['REQUEST_METHOD'] ?? 'GET';

if ($method === 'POST' && $uri === '/items') {
    http_response_code(201);
    header('Content-Type: application/json');
    echo json_encode([
        'route' => 'items.store',
        'method' => $method,
        'body' => file_get_contents('php://input'),
        'proto' => $_SERVER['HTTP_X_FORWARDED_PROTO'] ?? '',
        'host' => $_SERVER['HTTP_X_FORWARDED_HOST'] ?? '',
    ]);
    exit;
}

$routes = [
    '/' => 'root',
    '/deep/path' => 'deep',
    '/headers' => 'headers',
];

if (isset($routes[$uri]) && $method === 'GET') {
    http_response_code(200);
    header('Content-Type: application/json');
    echo json_encode([
        'route' => $routes[$uri],
        'uri' => $_SERVER['REQUEST_URI'] ?? '',
        'query' => $_GET,
        'method' => $method,
        'proto' => $_SERVER['HTTP_X_FORWARDED_PROTO'] ?? '',
        'host' => $_SERVER['HTTP_X_FORWARDED_HOST'] ?? '',
        'path_info' => $_SERVER['PATH_INFO'] ?? '',
    ]);
    exit;
}

http_response_code(404);
header('Content-Type: application/json');
echo json_encode(['error' => 'not found', 'uri' => $uri]);
