package textnorm

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"ЗВЁЗДЫ В ЛУЖАХ +":         "звезды в лужах",
		"IT'S OVER NOW":            "its over now",
		"AC/DC":                    "ac dc",
		"STEVENS, RAY":             "stevens ray",
		"Beyoncé":                  "beyonce",
		"ЙОГУРТ":                   "йогурт",
		"  GIVE ME (UNA NOCHE)  ":  "give me una noche",
		"3.15":                     "3 15",
		"DOGGIN’ AROUND":           "doggin around",
		"GILMER, JIMMY & THE BAND": "gilmer jimmy the band",
		"ПЕ\u00adРЕ\u200bХОД":      "переход",
		"ЗВЁ\u0301ЗДЫ\u00a0НЕБА":   "звезды неба",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCompact(t *testing.T) {
	if got := Compact("AC/DC"); got != "acdc" {
		t.Errorf("Compact = %q", got)
	}
}

func TestSwitchLayout(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ghbdtn", "привет"},
		{"f[fhfy", "ахаран"},
		{"ифтвщ", "bando"},
		{"ьфвщттф", "madonna"},
	}
	for _, c := range cases {
		got, ok := SwitchLayout(c.in)
		if !ok || got != c.want {
			t.Errorf("SwitchLayout(%q) = %q, %v; want %q", c.in, got, ok, c.want)
		}
	}
	if _, ok := SwitchLayout("123"); ok {
		t.Error("digits must not be switched")
	}
}
