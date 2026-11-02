package services

import (
	"strings"
	"testing"
)

func TestPhpMyAdminUserConfigUsesRootConfigAuth(t *testing.T) {
	for _, want := range []string{
		"$cfg['Servers'][1]['host'] = 'db';",
		"$cfg['Servers'][1]['port'] = '3306';",
		"$cfg['Servers'][1]['auth_type'] = 'config';",
		"$cfg['Servers'][1]['user'] = 'root';",
		"file_get_contents('/run/secrets/root_password')",
	} {
		if !strings.Contains(PhpMyAdminUserConfig, want) {
			t.Errorf("PhpMyAdminUserConfig missing %q:\n%s", want, PhpMyAdminUserConfig)
		}
	}
	if strings.Contains(PhpMyAdminUserConfig, "auth_type'] = 'cookie'") {
		t.Error("PhpMyAdminUserConfig still uses cookie authentication")
	}
	for _, forbidden := range []string{"root_password =", "devstack_password =", "password' = '"} {
		if strings.Contains(PhpMyAdminUserConfig, forbidden) {
			t.Errorf("PhpMyAdminUserConfig contains literal credential material %q:\n%s", forbidden, PhpMyAdminUserConfig)
		}
	}
}
