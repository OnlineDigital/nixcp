package nginxsnippet

import "testing"

func TestValidateAllowsScopedLocationDirectives(t *testing.T) {
	for _, snippet := range []string{
		"try_files $uri $uri/ /index.php?$query_string;\nadd_header X-Frame-Options SAMEORIGIN always;",
		"rewrite ^/old/(.*)$ /new/$1 permanent; # comment\nclient_max_body_size 20m;",
		"return 302 \"https://example.test/a;still-an-argument\";",
	} {
		if err := Validate(snippet); err != nil {
			t.Fatalf("Validate(%q): %v", snippet, err)
		}
	}
}

func TestValidateRejectsEscapesAndDangerousDirectives(t *testing.T) {
	for _, snippet := range []string{
		"server { listen 80; }", "location /x { return 200; }", "} server {",
		"include /tmp/attacker.conf;", "root /srv/other;", "fastcgi_pass unix:/tmp/x;",
		"proxy_pass http://attacker;", "try_files $uri", "if ($x) { return 200; }",
	} {
		if err := Validate(snippet); err == nil {
			t.Fatalf("Validate(%q) unexpectedly succeeded", snippet)
		}
	}
}
