package redact

import (
	"strings"
	"testing"
)

const id = "9f14f259-576c-41ad-90f9-783e6d831455"

func TestUUID(t *testing.T) {
	if got := UUID(id); got != "9f••••55" {
		t.Fatalf("UUID = %q", got)
	}
	if got := UUID(strings.ToUpper(id)); strings.Contains(got, "576C") || got != "9F••••55" {
		t.Fatalf("upper-case UUID not masked: %q", got)
	}
	if UUID("") != "" || UUID("abc") != "••••" || UUID("drone-01") != "dr••••01" || UUID("drone1") != "••••" {
		t.Fatal("non-UUID values must be shortened")
	}
}

func TestUUIDs(t *testing.T) {
	in := "rtsp://host:8554/live/" + id + "/cam0 and " + id
	got := UUIDs(in)
	if strings.Contains(got, "14f2") || strings.Contains(got, "1455") || strings.Count(got, "9f••••55") != 2 {
		t.Fatalf("UUIDs = %q", got)
	}
	if UUIDs("no ids - here - at - all") != "no ids - here - at - all" {
		t.Fatal("text without UUID changed")
	}
}

func TestHint(t *testing.T) {
	if Hint("") != "" || Hint("short") != "••••" || Hint("abcdefghijklmnop") != "••••mnop" {
		t.Fatal("Hint wrong")
	}
}
