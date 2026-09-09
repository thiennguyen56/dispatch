package config

import (
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	t.Parallel()

	got := Default()
	if want := ":8080"; got.Server.Address != want {
		t.Errorf("server address = %q, want %q", got.Server.Address, want)
	}
	if want := 15 * time.Second; got.Server.ReadTimeout != want {
		t.Errorf("read timeout = %s, want %s", got.Server.ReadTimeout, want)
	}
	if want := 15 * time.Second; got.Server.WriteTimeout != want {
		t.Errorf("write timeout = %s, want %s", got.Server.WriteTimeout, want)
	}
}
