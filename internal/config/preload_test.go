package config

import (
	"reflect"
	"testing"
	"time"
)

func TestParsePreloadFlagsOrderAndModes(t *testing.T) {
	cfg, err := ParseArgs([]string{
		"--preload", "a.log",
		"--preload-raw", "api=b.log",
		"--preload-capture", "screen-x.txt",
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := []PreloadSpec{
		{Group: "", Path: "a.log", Mode: PreloadAuto},
		{Group: "api", Path: "b.log", Mode: PreloadRaw},
		{Group: "", Path: "screen-x.txt", Mode: PreloadCapture},
	}
	if !reflect.DeepEqual(cfg.Preloads, want) {
		t.Errorf("Preloads = %+v, want %+v", cfg.Preloads, want)
	}
}

func TestParsePreloadValueWindowsPath(t *testing.T) {
	if g, p := parsePreloadValue(`C:\logs\a.log`); g != "" || p != `C:\logs\a.log` {
		t.Errorf("bare windows path → %q,%q", g, p)
	}
	if g, p := parsePreloadValue(`api=C:\logs\a.log`); g != "api" || p != `C:\logs\a.log` {
		t.Errorf("group=windows path → %q,%q", g, p)
	}
	if g, p := parsePreloadValue(`plain.log`); g != "" || p != `plain.log` {
		t.Errorf("plain → %q,%q", g, p)
	}
}
