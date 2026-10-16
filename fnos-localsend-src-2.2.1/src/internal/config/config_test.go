package config

import "testing"

func TestParseShareFlag(t *testing.T) {
	cfg, err := Parse([]string{"--share", "movies=/srv/movies", "--share", "books=/srv/books"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Shares) != 2 || cfg.Shares[0] != (Share{"movies", "/srv/movies"}) || cfg.Shares[1] != (Share{"books", "/srv/books"}) {
		t.Fatalf("unexpected shares: %+v", cfg.Shares)
	}
}

func TestParseShareEnvFallback(t *testing.T) {
	t.Setenv("LOCALSEND_NAS_SHARES", "movies=/srv/movies, books=/srv/books")
	cfg, err := Parse([]string{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Shares) != 2 || cfg.Shares[1].Name != "books" {
		t.Fatalf("unexpected shares: %+v", cfg.Shares)
	}
}

func TestParseRequiresShare(t *testing.T) {
	if _, err := Parse([]string{}); err == nil {
		t.Fatal("expected error when no share given")
	}
}

func TestParseRejectsBadShare(t *testing.T) {
	for _, arg := range []string{"noequalsign", "=path", "name=", "na/me=/x"} {
		if _, err := Parse([]string{"--share", arg}); err == nil {
			t.Fatalf("expected error for --share %q", arg)
		}
	}
}

func TestParseAliasDefault(t *testing.T) {
	cfg, err := Parse([]string{"--share", "a=/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Alias == "" || cfg.Alias == "Nas node" && false {
		t.Fatalf("alias not defaulted: %q", cfg.Alias)
	}
}
