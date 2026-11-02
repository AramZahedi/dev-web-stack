package services

import (
	"strings"
	"testing"
)

func TestPhpMyAdminUserConfigUsesTokenSignon(t *testing.T) {
	for _, want := range []string{
		"$cfg['Servers'][1]['host'] = 'db';",
		"$cfg['Servers'][1]['port'] = '3306';",
		"$cfg['Servers'][1]['auth_type'] = 'signon';",
		"$cfg['Servers'][1]['SignonSession'] = 'devstack_signon';",
		"/signon.php",
	} {
		if !strings.Contains(PhpMyAdminUserConfig, want) {
			t.Errorf("PhpMyAdminUserConfig missing %q:\n%s", want, PhpMyAdminUserConfig)
		}
	}
	for _, forbidden := range []string{"auth_type'] = 'config'", "auth_type'] = 'cookie'", "password'] = '"} {
		if strings.Contains(PhpMyAdminUserConfig, forbidden) {
			t.Errorf("PhpMyAdminUserConfig still contains %q:\n%s", forbidden, PhpMyAdminUserConfig)
		}
	}
}

func TestPhpMyAdminSignonScriptValidatesTokenAndRendersManualForm(t *testing.T) {
	for _, want := range []string{
		"devstack_signon_verify_token",
		"hash_equals($expected, $signature)",
		"devstack_signon_consume_nonce",
		"PMA_single_signon_user",
		"/run/secrets/root_password",
		"<form method=\"post\" action=\"signon.php\">",
	} {
		if !strings.Contains(PhpMyAdminSignonScript, want) {
			t.Errorf("PhpMyAdminSignonScript missing %q", want)
		}
	}
	if strings.Contains(PhpMyAdminSignonScript, "'config'") {
		t.Error("signon script still renders config authentication")
	}
}
