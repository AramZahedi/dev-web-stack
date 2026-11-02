package services

// PhpMyAdminSignonScript is mounted at /var/www/html/signon.php. It validates
// a short-lived HMAC token issued by the dashboard, consumes its nonce once,
// and stores the active root credential in the phpMyAdmin signon session.
// Without a valid token it renders the normal manual login form.
const PhpMyAdminSignonScript = `<?php
declare(strict_types=1);

const DEVSTACK_SIGNON_SESSION = 'devstack_signon';
const DEVSTACK_SIGNON_KEY = '/run/secrets/signon_key';
const DEVSTACK_SIGNON_PASSWORD = '/run/secrets/root_password';

function devstack_signon_base64url_decode(string $value): string|false
{
    $value = strtr($value, '-_', '+/');
    $padding = strlen($value) % 4;
    if ($padding !== 0) {
        $value .= str_repeat('=', 4 - $padding);
    }
    return base64_decode($value, true);
}

function devstack_signon_fail(string $message, int $status = 403): void
{
    http_response_code($status);
    header('Content-Type: text/html; charset=utf-8');
    header('Cache-Control: no-store');
    header('Referrer-Policy: no-referrer');
    header('X-Frame-Options: DENY');
    $escaped = htmlspecialchars($message, ENT_QUOTES | ENT_SUBSTITUTE, 'UTF-8');
    echo '<!doctype html><html lang="en"><head><meta charset="utf-8">';
    echo '<title>phpMyAdmin signon</title></head><body>';
    echo '<h1>phpMyAdmin signon</h1><p>' . $escaped . '</p></body></html>';
    exit;
}

function devstack_signon_consume_nonce(string $nonce): bool
{
    if (!preg_match('/^[a-f0-9]{32}$/', $nonce)) {
        return false;
    }
    $path = sys_get_temp_dir() . '/devstack-pma-signon-' . hash('sha256', $nonce);
    $handle = @fopen($path, 'x');
    if ($handle === false) {
        return false;
    }
    fwrite($handle, (string) time());
    fclose($handle);
    @chmod($path, 0600);
    return true;
}

function devstack_signon_verify_token(string $token): bool
{
    $parts = explode('.', $token);
    if (count($parts) !== 2) {
        return false;
    }
    $key = trim((string) @file_get_contents(DEVSTACK_SIGNON_KEY));
    if (strlen($key) < 32) {
        return false;
    }
    $payload = devstack_signon_base64url_decode($parts[0]);
    $signature = devstack_signon_base64url_decode($parts[1]);
    if ($payload === false || $signature === false) {
        return false;
    }
    $expected = hash_hmac('sha256', $parts[0], $key, true);
    if (!hash_equals($expected, $signature)) {
        return false;
    }
    $data = json_decode($payload, true);
    if (!is_array($data)) {
        return false;
    }
    $now = time();
    if (($data['v'] ?? 0) !== 1 || ($data['sub'] ?? '') !== 'root') {
        return false;
    }
    if (!is_int($data['iat'] ?? null) || !is_int($data['exp'] ?? null)) {
        return false;
    }
    if ($data['iat'] > $now + 5 || $data['exp'] < $now || $data['exp'] - $data['iat'] > 120) {
        return false;
    }
    return devstack_signon_consume_nonce((string) ($data['nonce'] ?? ''));
}

function devstack_signon_start_session(): void
{
    ini_set('session.use_cookies', 'true');
    ini_set('session.use_only_cookies', 'true');
    session_set_cookie_params([
        'lifetime' => 0,
        'path' => '/',
        'secure' => false,
        'httponly' => true,
        'samesite' => 'Strict',
    ]);
    session_name(DEVSTACK_SIGNON_SESSION);
    if (session_status() !== PHP_SESSION_ACTIVE) {
        @session_start();
    }
}

function devstack_signon_redirect(): void
{
    header('Cache-Control: no-store');
    header('Referrer-Policy: no-referrer');
    header('Location: index.php');
    exit;
}

devstack_signon_start_session();

if ($_SERVER['REQUEST_METHOD'] === 'POST' && isset($_POST['token'])) {
    $token = (string) $_POST['token'];
    if (!devstack_signon_verify_token($token)) {
        devstack_signon_fail('This signon link is invalid, expired, or has already been used.');
    }
    $password = trim((string) @file_get_contents(DEVSTACK_SIGNON_PASSWORD));
    if ($password === '') {
        devstack_signon_fail('The active database root secret is unavailable.', 503);
    }
    $_SESSION['PMA_single_signon_user'] = 'root';
    $_SESSION['PMA_single_signon_password'] = $password;
    $_SESSION['PMA_single_signon_host'] = 'db';
    $_SESSION['PMA_single_signon_port'] = '3306';
    $_SESSION['PMA_single_signon_HMAC_secret'] = bin2hex(random_bytes(16));
    session_write_close();
    devstack_signon_redirect();
}

if ($_SERVER['REQUEST_METHOD'] === 'POST') {
    $csrf = (string) ($_POST['csrf'] ?? '');
    $expected = (string) ($_SESSION['devstack_signon_csrf'] ?? '');
    if ($csrf === '' || $expected === '' || !hash_equals($expected, $csrf)) {
        devstack_signon_fail('The login form expired. Reload the page and try again.');
    }
    $user = trim((string) ($_POST['user'] ?? ''));
    $password = (string) ($_POST['password'] ?? '');
    if ($user === '' || $password === '') {
        devstack_signon_fail('Username and password are required.', 400);
    }
    $_SESSION['PMA_single_signon_user'] = $user;
    $_SESSION['PMA_single_signon_password'] = $password;
    $_SESSION['PMA_single_signon_host'] = 'db';
    $_SESSION['PMA_single_signon_port'] = '3306';
    $_SESSION['PMA_single_signon_HMAC_secret'] = bin2hex(random_bytes(16));
    session_write_close();
    devstack_signon_redirect();
}

$_SESSION['devstack_signon_csrf'] = bin2hex(random_bytes(16));
$csrf = $_SESSION['devstack_signon_csrf'];
$error = (string) ($_SESSION['PMA_single_signon_error_message'] ?? '');
unset($_SESSION['PMA_single_signon_error_message']);
session_write_close();
header('Content-Type: text/html; charset=utf-8');
header('Cache-Control: no-store');
header('Referrer-Policy: no-referrer');
header('X-Frame-Options: DENY');
?>
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>phpMyAdmin signon</title>
<style>
body{font-family:system-ui,sans-serif;max-width:26rem;margin:4rem auto;padding:0 1rem}
label{display:block;margin:.75rem 0 .25rem}
input{width:100%;padding:.5rem;box-sizing:border-box}
button{margin-top:1rem;padding:.6rem 1rem}
.error{color:#b00020}
</style>
</head>
<body>
<h1>phpMyAdmin</h1>
<form id="signon" method="post" action="signon.php">
<input type="hidden" name="token" id="signon-token">
</form>
<script>
(function () {
  const match = window.location.hash.match(/^#token=(.+)$/);
  if (!match) return;
  document.getElementById('signon-token').value = decodeURIComponent(match[1]);
  window.history.replaceState(null, '', window.location.pathname);
  document.getElementById('signon').submit();
})();
</script>
<p>Open this page from the devstack dashboard for automatic sign-in, or use the form below.</p>
<?php if ($error !== ''): ?><p class="error"><?= htmlspecialchars($error, ENT_QUOTES | ENT_SUBSTITUTE, 'UTF-8') ?></p><?php endif; ?>
<form method="post" action="signon.php">
<input type="hidden" name="csrf" value="<?= htmlspecialchars($csrf, ENT_QUOTES, 'UTF-8') ?>">
<label for="user">Username</label>
<input id="user" name="user" autocomplete="username" spellcheck="false" required>
<label for="password">Password</label>
<input id="password" name="password" type="password" autocomplete="current-password" required>
<button type="submit">Sign in</button>
</form>
</body>
</html>
`
